// Command sigild is the Sigil OS daemon — a self-tuning intelligence layer
// that observes workflow patterns, builds a local user model, and reshapes
// the developer environment.
//
// Phase 1 (v0) capabilities:
//   - Collector: file events, process events, git activity, terminal commands
//   - Store: SQLite (WAL mode) — all data stays local
//   - Analyzer: hourly heuristic pass + inference engine LLM summary
//   - Notifier: 5-level suggestion surfacing via desktop notifications
//   - Socket: Unix domain socket for sigilctl and the future shell
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"

	"github.com/wambozi/sigil/internal/actuator"
	"github.com/wambozi/sigil/internal/analyzer"
	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
	"github.com/wambozi/sigil/internal/config"
	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/fleet"
	"github.com/wambozi/sigil/internal/inference"
	"github.com/wambozi/sigil/internal/network"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/socket"
	"github.com/wambozi/sigil/internal/store"
)

func main() {
	// Check for subcommands before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := cmdInit(); err != nil {
			fmt.Fprintln(os.Stderr, "sigild init:", err)
			os.Exit(1)
		}
		return
	}

	cfg := parseFlags()

	log := newLogger(cfg.logLevel)
	log.Info("sigild starting", "version", "0.1.0-dev")

	if err := run(cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// --- Config -----------------------------------------------------------------

type daemonConfig struct {
	dbPath        string
	socketPath    string
	inferenceMode string
	watchPaths    []string
	repoPaths     []string
	maxWatches    int
	analyzeEvery  time.Duration
	notifierLevel int
	logLevel      string
	digestTime    string
	fileCfg       *config.Config // resolved file config (for handlers that need it)
}

func parseFlags() daemonConfig {
	// Load file-based config first; flags override these values.
	cfgPath := flag.String("config", config.DefaultPath(), "Path to TOML config file")
	// Pre-parse just the -config flag so we can load the file before defining
	// the remaining flags with file-sourced defaults.
	flag.CommandLine.Parse(os.Args[1:]) //nolint // full parse happens below
	fileCfg, err := config.Load(*cfgPath)
	if err != nil {
		// Non-fatal: log to stderr and continue with defaults.
		fmt.Fprintf(os.Stderr, "sigild: config load warning: %v\n", err)
		fileCfg = config.Defaults()
	}

	// Reset flags so we can define them with file-sourced defaults.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Re-define -config so the second parse doesn't fail on it.
	_ = flag.String("config", *cfgPath, "Path to TOML config file")

	dbDefault := fileCfg.Daemon.DBPath
	if dbDefault == "" {
		dbDefault = defaultDBPath()
	}
	sockDefault := fileCfg.Daemon.SocketPath
	if sockDefault == "" {
		sockDefault = defaultSocketPath()
	}
	watchDefault := strings.Join(fileCfg.Daemon.WatchDirs, ",")
	if watchDefault == "" {
		watchDefault = homeDir()
	}
	repoDefault := strings.Join(fileCfg.Daemon.RepoDirs, ",")
	if repoDefault == "" {
		repoDefault = homeDir()
	}

	var (
		dbPath        = flag.String("db", dbDefault, "SQLite database path")
		socketPath    = flag.String("socket", sockDefault, "Unix socket path")
		inferenceMode = flag.String("inference-mode", fileCfg.Inference.Mode, "Inference routing mode: local|localfirst|remotefirst|remote")
		watchPaths    = flag.String("watch", watchDefault, "Comma-separated directories to watch for file events")
		repoPaths     = flag.String("repos", repoDefault, "Comma-separated git repository roots to watch")
		analyzeEvery  = flag.Duration("analyze-every", time.Hour, "How often to run an analysis cycle")
		notifierLevel = flag.Int("level", fileCfg.Notifier.Level, "Notification level 0=silent 1=digest 2=ambient 3=conversational 4=autonomous")
		logLevel      = flag.String("log-level", fileCfg.Daemon.LogLevel, "Log level: debug|info|warn|error")
		digestTime    = flag.String("digest-time", fileCfg.Notifier.DigestTime, "Daily digest time HH:MM (level 1 only)")
	)
	flag.Parse()

	return daemonConfig{
		dbPath:        *dbPath,
		socketPath:    *socketPath,
		inferenceMode: *inferenceMode,
		watchPaths:    splitPaths(*watchPaths),
		repoPaths:     splitPaths(*repoPaths),
		maxWatches:    fileCfg.Daemon.MaxWatches,
		analyzeEvery:  *analyzeEvery,
		notifierLevel: *notifierLevel,
		logLevel:      *logLevel,
		digestTime:    *digestTime,
		fileCfg:       fileCfg,
	}
}

// --- Runtime ----------------------------------------------------------------

func run(cfg daemonConfig, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- Store --------------------------------------------------------------
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o700); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()
	log.Info("store opened", "path", cfg.dbPath)

	// --- Inference engine ----------------------------------------------------
	infCfg := inference.Config{
		Mode: inference.RoutingMode(cfg.inferenceMode),
		Local: inference.LocalConfig{
			Enabled:   cfg.fileCfg.Inference.Local.Enabled,
			ServerURL: cfg.fileCfg.Inference.Local.ServerURL,
			ServerBin: cfg.fileCfg.Inference.Local.ServerBin,
			ModelPath: cfg.fileCfg.Inference.Local.ModelPath,
			CtxSize:   cfg.fileCfg.Inference.Local.CtxSize,
			GPULayers: cfg.fileCfg.Inference.Local.GPULayers,
		},
		Cloud: inference.CloudConfig{
			Enabled:  cfg.fileCfg.Inference.Cloud.Enabled,
			Provider: cfg.fileCfg.Inference.Cloud.Provider,
			BaseURL:  cfg.fileCfg.Inference.Cloud.BaseURL,
			APIKey:   cfg.fileCfg.Inference.Cloud.APIKey,
			Model:    cfg.fileCfg.Inference.Cloud.Model,
		},
	}
	engine, err := inference.New(infCfg, log)
	if err != nil {
		return fmt.Errorf("create inference engine: %w", err)
	}
	defer engine.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := engine.Ping(pingCtx); err != nil {
		log.Warn("inference engine unreachable at startup — will retry before each cloud pass",
			"mode", cfg.inferenceMode, "err", err)
	} else {
		log.Info("inference engine reachable", "mode", cfg.inferenceMode)
	}
	cancel()

	// --- Collector ----------------------------------------------------------
	terminalSrc := sources.NewTerminalSource()

	col := collector.New(db, log)
	col.Add(&sources.FileSource{Paths: cfg.watchPaths, IgnorePatterns: cfg.fileCfg.Daemon.IgnorePatterns, MaxWatches: cfg.maxWatches})
	col.Add(&sources.ProcessSource{})
	col.Add(&sources.GitSource{RepoPaths: cfg.repoPaths})
	col.Add(terminalSrc)
	col.Add(&sources.HyprlandSource{})

	if err := col.Start(ctx); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}
	log.Info("collector started", "sources", 5)

	// --- Notifier -----------------------------------------------------------
	ntf := notifier.New(db, notifier.Level(cfg.notifierLevel), log)
	log.Info("notifier started", "level", cfg.notifierLevel)

	// --- Analyzer -----------------------------------------------------------
	anlz := analyzer.New(db, engine, cfg.analyzeEvery, log)
	anlz.OnSummary = func(s analyzer.Summary) {
		// Surface each locally-detected pattern suggestion.
		for _, sg := range s.Suggestions {
			ntf.Surface(sg)
		}

		// Surface the LLM-generated narrative when present.
		if s.Insights != "" {
			ntf.Surface(notifier.Suggestion{
				Category:   "insight",
				Confidence: notifier.ConfidenceModerate,
				Title:      "Sigil workflow summary",
				Body:       s.Insights,
			})
		}
	}

	go anlz.Run(ctx)
	log.Info("analyzer started", "interval", cfg.analyzeEvery)

	// --- RSS monitor --------------------------------------------------------
	var currentRSSMB atomic.Int64
	go runRSSMonitor(ctx, log, &currentRSSMB)

	// --- Daily digest scheduler ---------------------------------------------
	nextDigest := scheduleDigest(ctx, cfg.digestTime, ntf, log)

	// --- Keybinding profile tracking ----------------------------------------
	var currentProfile atomic.Value
	currentProfile.Store("terminal") // default profile

	// --- Socket server ------------------------------------------------------
	srv := socket.New(cfg.socketPath, log)
	registerHandlers(srv, db, engine, ntf, anlz, terminalSrc, log, &currentRSSMB, nextDigest, &currentProfile, cfg)

	// Wire suggestion push via the notifier's OnSuggestion callback.
	// Every suggestion that passes the confidence gate is fanned out to any
	// socket connections subscribed to the "suggestions" topic.
	ntf.OnSuggestion = func(id int64, sg notifier.Suggestion) {
		payload := socket.MarshalPayload(map[string]any{
			"id":         id,
			"text":       sg.Body,
			"title":      sg.Title,
			"confidence": sg.Confidence,
			"action_cmd": sg.ActionCmd,
		})
		srv.Notify("suggestions", payload)
	}
	ntf.HasExternalSurface = func() bool {
		return srv.SubscriberCount("suggestions") > 0
	}

	// --- Actuator registry --------------------------------------------------
	actuatorNotify := func(a actuator.Action) {
		payload := socket.MarshalPayload(map[string]any{
			"type":        "actuation",
			"id":          a.ID,
			"description": a.Description,
			"undo_cmd":    a.UndoCmd,
		})
		srv.Notify("actuations", payload)
	}

	reg := actuator.New(db, actuatorNotify, log)
	go reg.Run(ctx)

	// Build-split actuator: event-driven, reads from collector.Broadcast.
	buildSplit := actuator.NewBuildSplitActuator(log)
	go buildSplit.RunEventLoop(col.Broadcast, func(a actuator.Action, typ string) {
		reg.Notify(a)
		// Also send the specific split-pane/close-split type for the shell.
		payload := socket.MarshalPayload(map[string]any{
			"type":   typ,
			"reason": "build_" + typ,
		})
		srv.Notify("actuations", payload)
	})

	log.Info("actuator registry started")

	// --- Fleet Reporter -----------------------------------------------------
	var fleetReporter *fleet.Reporter
	if cfg.fileCfg.Fleet.Enabled {
		fleetReporter = fleet.New(db, cfg.fileCfg.Fleet, log)
		go fleetReporter.Run(ctx)
		log.Info("fleet reporter started", "endpoint", cfg.fileCfg.Fleet.Endpoint)
	}
	registerFleetHandlers(srv, fleetReporter)

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start socket: %w", err)
	}
	log.Info("socket listening", "path", cfg.socketPath)

	// --- Credential store (always initialised; handlers registered below) ---
	credStore := network.NewCredentialStore()
	credsPath := filepath.Join(networkDataDir(), "credentials.json")
	if err := credStore.LoadFromFile(credsPath); err != nil {
		log.Warn("credential store: load failed — starting empty", "err", err)
	}

	// --- Optional TCP+TLS listener ------------------------------------------
	var tlsCert *tls.Certificate
	var spki string
	if cfg.fileCfg.Network.Enabled {
		netDir := networkDataDir()
		cert, err := network.LoadOrGenerate(netDir)
		if err != nil {
			return fmt.Errorf("network: TLS cert: %w", err)
		}
		tlsCert = &cert

		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return fmt.Errorf("network: parse cert: %w", err)
		}
		spki = network.SPKIFingerprint(leaf)

		bind := cfg.fileCfg.Network.Bind
		if bind == "" {
			bind = "0.0.0.0"
		}
		port := cfg.fileCfg.Network.Port
		if port == 0 {
			port = 7773
		}
		addr := fmt.Sprintf("%s:%d", bind, port)

		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}
		ln, err := tls.Listen("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("network: listen %s: %w", addr, err)
		}
		authLn := network.NewAuthListener(ln, credStore)
		if err := srv.ServeListener(ctx, authLn); err != nil {
			return fmt.Errorf("network: serve: %w", err)
		}
		log.Info("network listener started", "addr", addr, "spki", spki)
	}

	// --- Credential socket handlers (Unix socket only) ----------------------
	registerCredentialHandlers(srv, credStore, credsPath, tlsCert, spki, cfg.fileCfg.Network, log)

	// --- Wait for shutdown --------------------------------------------------
	<-ctx.Done()
	log.Info("shutdown signal received")

	drainWithTimeout(log, 10*time.Second, func() {
		srv.Stop()
		col.Stop()
	})

	log.Info("sigild stopped cleanly")
	return nil
}

// drainWithTimeout runs drainFn in a goroutine and waits up to timeout for it
// to complete.  If it exceeds the deadline it logs an error and calls
// os.Exit(1) so systemd will restart the daemon.
func drainWithTimeout(log *slog.Logger, timeout time.Duration, drainFn func()) {
	drainWithTimeoutAndExit(log, timeout, drainFn, func(code int) { os.Exit(code) })
}

// drainWithTimeoutAndExit is the testable core of drainWithTimeout.
// exitFn replaces os.Exit so tests can observe the timeout without forking.
func drainWithTimeoutAndExit(log *slog.Logger, timeout time.Duration, drainFn func(), exitFn func(int)) {
	done := make(chan struct{})
	go func() {
		drainFn()
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown.
	case <-time.After(timeout):
		log.Error("shutdown drain timed out — forcing exit",
			"timeout", timeout)
		exitFn(1)
	}
}

// --- Socket handlers --------------------------------------------------------

// registerHandlers wires all socket methods to their implementations.
// Each handler runs in a per-connection goroutine; all store calls must be
// context-aware so they respect connection-level cancellation.
func registerHandlers(
	srv *socket.Server,
	db *store.Store,
	engine *inference.Engine,
	ntf *notifier.Notifier,
	anlz *analyzer.Analyzer,
	terminalSrc *sources.TerminalSource,
	log *slog.Logger,
	rssMB *atomic.Int64,
	nextDigest *atomic.Int64,
	currentProfile *atomic.Value,
	cfg daemonConfig,
) {
	// status — quick health check for sigilctl and the shell.
	srv.Handle("status", func(ctx context.Context, _ socket.Request) socket.Response {
		payload := map[string]any{
			"status":                     "ok",
			"version":                    "0.1.0-dev",
			"notifier_level":             int(ntf.Level()),
			"rss_mb":                     rssMB.Load(),
			"current_keybinding_profile": currentProfile.Load(),
		}
		if ntf.Level() == notifier.LevelDigest {
			if ns := nextDigest.Load(); ns > 0 {
				payload["next_digest_at"] = time.Unix(ns, 0).UTC().Format(time.RFC3339)
			}
		}
		return socket.Response{
			OK:      true,
			Payload: socket.MarshalPayload(payload),
		}
	})

	// events — return recent events from the store.
	srv.Handle("events", func(ctx context.Context, req socket.Request) socket.Response {
		events, err := db.QueryEvents(ctx, "", 50)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(events)}
	})

	// suggestions — return recent suggestions from the store.
	srv.Handle("suggestions", func(ctx context.Context, _ socket.Request) socket.Response {
		suggestions, err := db.QuerySuggestions(ctx, "", 50)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(suggestions)}
	})

	// set-level — change the notifier level at runtime.
	// Payload: {"level": 2}
	srv.Handle("set-level", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Level int `json:"level"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.Level < 0 || p.Level > 4 {
			return socket.Response{Error: "level must be 0-4"}
		}
		ntf.SetLevel(notifier.Level(p.Level))
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"level": p.Level})}
	})

	// ingest — called by the shell hook after each command.
	// Payload: {"cmd":"...","exit_code":0,"cwd":"/home/...","ts":1234567890,"session_id":"..."}
	srv.Handle("ingest", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Cmd       string `json:"cmd"`
			ExitCode  int    `json:"exit_code"`
			Cwd       string `json:"cwd"`
			Ts        int64  `json:"ts"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid ingest payload: " + err.Error()}
		}
		if strings.TrimSpace(p.Cmd) == "" {
			return socket.Response{Error: "cmd is required"}
		}

		ts := time.Now()
		if p.Ts > 0 {
			ts = time.Unix(p.Ts, 0)
		}

		payload := map[string]any{
			"cmd":       p.Cmd,
			"exit_code": p.ExitCode,
			"cwd":       p.Cwd,
		}
		if p.SessionID != "" {
			payload["session_id"] = p.SessionID
		}

		terminalSrc.Ingest(event.Event{
			Kind:      event.KindTerminal,
			Source:    "terminal",
			Payload:   payload,
			Timestamp: ts,
		})

		log.Debug("terminal event ingested", "cmd", p.Cmd, "exit_code", p.ExitCode, "cwd", p.Cwd)
		return socket.Response{OK: true}
	})

	// files — top files edited in the last 24 hours by edit count.
	srv.Handle("files", func(ctx context.Context, _ socket.Request) socket.Response {
		files, err := db.QueryTopFiles(ctx, time.Now().Add(-24*time.Hour), 20)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query top files: %s", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(files)}
	})

	// commands — command frequency table for the last 24 hours.
	// Tallies by command string and tracks the last observed exit code.
	srv.Handle("commands", func(ctx context.Context, _ socket.Request) socket.Response {
		events, err := db.QueryTerminalEvents(ctx, time.Now().Add(-24*time.Hour))
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query terminal events: %s", err)}
		}

		type entry struct {
			count        int
			lastExitCode int
		}
		tally := make(map[string]*entry)

		for _, e := range events {
			cmd, _ := e.Payload["cmd"].(string)
			if cmd == "" {
				continue
			}
			// exit_code is stored as float64 after JSON round-trip through map[string]any.
			exitCode := 0
			if v, ok := e.Payload["exit_code"]; ok {
				switch ec := v.(type) {
				case float64:
					exitCode = int(ec)
				case int:
					exitCode = ec
				}
			}
			if ent, exists := tally[cmd]; exists {
				ent.count++
				ent.lastExitCode = exitCode
			} else {
				tally[cmd] = &entry{count: 1, lastExitCode: exitCode}
			}
		}

		type row struct {
			Cmd          string `json:"cmd"`
			Count        int    `json:"count"`
			LastExitCode int    `json:"last_exit_code"`
		}
		rows := make([]row, 0, len(tally))
		for cmd, ent := range tally {
			rows = append(rows, row{
				Cmd:          cmd,
				Count:        ent.count,
				LastExitCode: ent.lastExitCode,
			})
		}
		// Sort descending by count so the most-used commands appear first.
		for i := 1; i < len(rows); i++ {
			for j := i; j > 0 && rows[j].Count > rows[j-1].Count; j-- {
				rows[j], rows[j-1] = rows[j-1], rows[j]
			}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(rows)}
	})

	// patterns — suggestions with category "pattern", most recent 50.
	// Patterns are stored as suggestions; the category field distinguishes them.
	srv.Handle("patterns", func(ctx context.Context, _ socket.Request) socket.Response {
		suggestions, err := db.QuerySuggestions(ctx, "", 50)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query patterns: %s", err)}
		}
		// Filter to category == "pattern" in Go so we don't need a separate
		// store method and can reuse the existing query.
		patterns := suggestions[:0]
		for _, sg := range suggestions {
			if sg.Category == "pattern" {
				patterns = append(patterns, sg)
			}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(patterns)}
	})

	// trigger-summary — immediately enqueues an analysis cycle.
	// The analyzer's Run loop drains the trigger channel on its next iteration.
	srv.Handle("trigger-summary", func(ctx context.Context, _ socket.Request) socket.Response {
		anlz.Trigger()
		return socket.Response{
			OK: true,
			Payload: socket.MarshalPayload(map[string]any{
				"ok":      true,
				"message": "analysis cycle queued",
			}),
		}
	})

	// feedback — record the outcome of a surfaced suggestion.
	// Payload: {"suggestion_id": 42, "outcome": "accepted"|"dismissed"}
	srv.Handle("feedback", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			SuggestionID int64  `json:"suggestion_id"`
			Outcome      string `json:"outcome"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.SuggestionID <= 0 {
			return socket.Response{Error: "suggestion_id must be a positive integer"}
		}
		switch p.Outcome {
		case "accepted", "dismissed":
		default:
			return socket.Response{Error: `outcome must be "accepted" or "dismissed"`}
		}

		var status store.SuggestionStatus
		if p.Outcome == "accepted" {
			status = store.StatusAccepted
		} else {
			status = store.StatusDismissed
		}

		if err := db.UpdateSuggestionStatus(ctx, p.SuggestionID, status); err != nil {
			return socket.Response{Error: fmt.Sprintf("update suggestion status: %s", err)}
		}
		if err := db.InsertFeedback(ctx, p.SuggestionID, p.Outcome); err != nil {
			return socket.Response{Error: fmt.Sprintf("insert feedback: %s", err)}
		}

		log.Info("feedback recorded", "suggestion_id", p.SuggestionID, "outcome", p.Outcome)
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"ok": true})}
	})

	// config — return the resolved runtime configuration as JSON.
	// Sensitive fields (API keys / tokens) are masked with "***".
	srv.Handle("config", func(ctx context.Context, _ socket.Request) socket.Response {
		payload := map[string]any{
			"db_path":        cfg.dbPath,
			"socket_path":    cfg.socketPath,
			"inference_mode": cfg.inferenceMode,
			"watch_paths":    cfg.watchPaths,
			"repo_paths":     cfg.repoPaths,
			"analyze_every":  cfg.analyzeEvery.String(),
			"notifier_level": cfg.notifierLevel,
			"log_level":      cfg.logLevel,
			"digest_time":    cfg.digestTime,
			"raw_event_days": cfg.fileCfg.Retention.RawEventDays,
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(payload)}
	})

	// ai-query — routes a natural-language query through the inference engine
	// and logs the interaction for fleet metrics.
	// Request:  {"method":"ai-query","payload":{"query":"...","context":"..."}}
	// Response: {"ok":true,"payload":{"response":"...","routing":"local|cloud","latency_ms":120}}
	srv.Handle("ai-query", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Query   string `json:"query"`
			Context string `json:"context"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if strings.TrimSpace(p.Query) == "" {
			return socket.Response{Error: "query is required"}
		}

		result, err := engine.Complete(ctx, "You are a developer workflow assistant.", p.Query)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("inference: %s", err)}
		}

		_ = db.InsertAIInteraction(ctx, event.AIInteraction{
			QueryText:     p.Query,
			QueryCategory: p.Context,
			Routing:       result.Routing,
			LatencyMS:     result.LatencyMS,
			Timestamp:     time.Now(),
		})

		return socket.Response{
			OK: true,
			Payload: socket.MarshalPayload(map[string]any{
				"response":   result.Content,
				"routing":    result.Routing,
				"latency_ms": result.LatencyMS,
			}),
		}
	})

	// purge — deletes all stored data and removes the database file.
	// Intended for privacy workflows; the daemon must be restarted afterward.
	srv.Handle("purge", func(ctx context.Context, _ socket.Request) socket.Response {
		if err := db.Purge(); err != nil {
			return socket.Response{Error: fmt.Sprintf("purge: %s", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"ok": true})}
	})

	// undo — executes the undo command for the most recent undoable action.
	srv.Handle("undo", func(ctx context.Context, _ socket.Request) socket.Response {
		actions, err := db.QueryUndoableActions(ctx)
		if err != nil || len(actions) == 0 {
			return socket.Response{Error: "no undoable actions"}
		}
		a := actions[len(actions)-1]
		if a.UndoCmd == "" {
			return socket.Response{Error: "action is not reversible"}
		}
		if err := exec.Command("sh", "-c", a.UndoCmd).Run(); err != nil {
			return socket.Response{Error: fmt.Sprintf("undo failed: %s", err)}
		}
		_ = db.MarkActionUndone(ctx, a.ID)
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"undone": a.Description,
		})}
	})

	// actions — returns recent undoable actions.
	srv.Handle("actions", func(ctx context.Context, _ socket.Request) socket.Response {
		actions, err := db.QueryUndoableActions(ctx)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(actions)}
	})

	// sessions — return active terminal sessions from the last 24 hours.
	srv.Handle("sessions", func(ctx context.Context, _ socket.Request) socket.Response {
		termEvents, err := db.QueryTerminalEvents(ctx, time.Now().Add(-24*time.Hour))
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query terminal events: %s", err)}
		}

		// Group by session_id inline (simple map loop, no analyzer import needed).
		type sessionInfo struct {
			CmdCount int    `json:"cmd_count"`
			FirstTS  int64  `json:"first_ts"`
			LastTS   int64  `json:"last_ts"`
			LastCwd  string `json:"last_cwd"`
		}
		sessions := make(map[string]*sessionInfo)
		for _, e := range termEvents {
			sid, _ := e.Payload["session_id"].(string)
			if sid == "" {
				sid = "_unknown"
			}
			si, ok := sessions[sid]
			if !ok {
				si = &sessionInfo{FirstTS: e.Timestamp.Unix()}
				sessions[sid] = si
			}
			si.CmdCount++
			si.LastTS = e.Timestamp.Unix()
			if cwd, _ := e.Payload["cwd"].(string); cwd != "" {
				si.LastCwd = cwd
			}
		}

		type sessionRow struct {
			SessionID string `json:"session_id"`
			CmdCount  int    `json:"cmd_count"`
			FirstTS   int64  `json:"first_ts"`
			LastTS    int64  `json:"last_ts"`
			LastCwd   string `json:"last_cwd"`
		}
		rows := make([]sessionRow, 0, len(sessions))
		for sid, si := range sessions {
			rows = append(rows, sessionRow{
				SessionID: sid,
				CmdCount:  si.CmdCount,
				FirstTS:   si.FirstTS,
				LastTS:    si.LastTS,
				LastCwd:   si.LastCwd,
			})
		}

		return socket.Response{OK: true, Payload: socket.MarshalPayload(rows)}
	})

	// view-changed — called by the shell when the active tool view changes.
	// Updates the keybinding profile and pushes an actuation event.
	srv.Handle("view-changed", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			View string `json:"view"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload"}
		}
		if p.View == "" {
			return socket.Response{Error: "view is required"}
		}
		currentProfile.Store(p.View)
		payload := socket.MarshalPayload(map[string]any{
			"type":    "keybinding-profile",
			"profile": p.View,
		})
		srv.Notify("actuations", payload)
		log.Info("keybinding profile changed", "profile", p.View)
		return socket.Response{OK: true}
	})
}

// --- RSS monitor ------------------------------------------------------------

const (
	rssWarnMB  = 100
	rssLimitMB = 150
)

// runRSSMonitor reads /proc/self/status every 5 minutes.
// > 100 MB: log warning and halve ProcessSource polling interval (best-effort).
// > 150 MB: log error and exit so systemd restarts with a clean heap.
func runRSSMonitor(ctx context.Context, log *slog.Logger, current *atomic.Int64) {
	// Seed initial reading immediately so status shows RSS from the start.
	if mb, err := readRSSMB(); err == nil {
		current.Store(mb)
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mb, err := readRSSMB()
			if err != nil {
				log.Warn("rss monitor: read failed", "err", err)
				continue
			}
			current.Store(mb)

			switch {
			case mb > rssLimitMB:
				log.Error("rss exceeds hard limit — exiting for restart",
					"rss_mb", mb, "limit_mb", rssLimitMB)
				os.Exit(0)
			case mb > rssWarnMB:
				log.Warn("rss exceeds soft limit — consider restarting if this persists",
					"rss_mb", mb, "warn_mb", rssWarnMB)
			}
		}
	}
}

// readRSSMB is implemented in rss_linux.go and rss_darwin.go.

// --- Daily digest scheduler -------------------------------------------------

// scheduleDigest starts a background goroutine that calls ntf.FlushDigest()
// at the configured local time each day.  It returns an atomic int64 holding
// the Unix timestamp of the next flush (for the status endpoint).
func scheduleDigest(ctx context.Context, digestTime string, ntf *notifier.Notifier, log *slog.Logger) *atomic.Int64 {
	var next atomic.Int64

	go func() {
		for {
			t := nextDigestTime(digestTime)
			next.Store(t.Unix())

			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(t)):
				ntf.FlushDigest()
				log.Info("digest flushed", "digest_time", digestTime)
			}
		}
	}()

	return &next
}

// nextDigestTime returns the next wall-clock time when the digest should fire,
// based on the HH:MM string.  If the time has already passed today, it returns
// tomorrow's occurrence.
func nextDigestTime(hhmm string) time.Time {
	now := time.Now()
	hour, minute := 9, 0 // safe defaults
	if len(hhmm) == 5 && hhmm[2] == ':' {
		h, herr := strconv.Atoi(hhmm[:2])
		m, merr := strconv.Atoi(hhmm[3:])
		if herr == nil && merr == nil && h >= 0 && h < 24 && m >= 0 && m < 60 {
			hour, minute = h, m
		}
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

// --- Init subcommand --------------------------------------------------------

// cmdInit is defined in init.go (issue #9).
// This stub keeps the build green until that issue is implemented.
func cmdInit() error {
	return runInit()
}

// --- Helpers ----------------------------------------------------------------

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func defaultDBPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".local", "share")
	}
	return filepath.Join(base, "sigild", "data.db")
}

func defaultSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sigild.sock")
	}
	// Linux: /run/user/<uid> is the conventional runtime dir.
	// macOS: /run doesn't exist — use os.TempDir() which returns
	// the per-user $TMPDIR (e.g. /var/folders/xx/.../T/).
	if goruntime.GOOS == "darwin" {
		return filepath.Join(os.TempDir(), "sigild.sock")
	}
	return fmt.Sprintf("/run/user/%d/sigild.sock", os.Getuid())
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func splitPaths(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- Network data directory -------------------------------------------------

func networkDataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".local", "share")
	}
	return filepath.Join(base, "sigil")
}

// --- Credential socket handlers (Unix socket only) --------------------------

func registerCredentialHandlers(
	srv *socket.Server,
	store *network.CredentialStore,
	credsPath string,
	tlsCert *tls.Certificate,
	spki string,
	netCfg config.NetworkConfig,
	log *slog.Logger,
) {
	// credential.add — generate a new token and return the credential bundle.
	srv.Handle("credential.add", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil || p.ID == "" {
			return socket.Response{Error: "payload must be {\"id\":\"...\"}"}
		}

		token, err := generateToken()
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("generate token: %s", err)}
		}

		if err := store.Add(p.ID, token); err != nil {
			return socket.Response{Error: err.Error()}
		}
		if err := store.SaveToFile(credsPath); err != nil {
			log.Warn("credential.add: save failed", "err", err)
		}

		bind := netCfg.Bind
		if bind == "" {
			bind = "0.0.0.0"
		}
		port := netCfg.Port
		if port == 0 {
			port = 7773
		}
		serverAddr := fmt.Sprintf("%s:%d", bind, port)

		bundle := map[string]any{
			"id":               p.ID,
			"token":            token,
			"server_addr":      serverAddr,
			"server_cert_spki": spki,
			"generated_at":     time.Now().UTC().Format(time.RFC3339),
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(bundle)}
	})

	// credential.list — return all credentials without token values.
	srv.Handle("credential.list", func(ctx context.Context, _ socket.Request) socket.Response {
		creds := store.List()
		type item struct {
			ID        string  `json:"id"`
			CreatedAt string  `json:"created_at"`
			Revoked   bool    `json:"revoked"`
			RevokedAt *string `json:"revoked_at,omitempty"`
		}
		out := make([]item, 0, len(creds))
		for _, c := range creds {
			i := item{
				ID:        c.ID,
				CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
				Revoked:   c.Revoked,
			}
			if c.RevokedAt != nil {
				s := c.RevokedAt.UTC().Format(time.RFC3339)
				i.RevokedAt = &s
			}
			out = append(out, i)
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"credentials": out})}
	})

	// credential.revoke — revoke by id.
	srv.Handle("credential.revoke", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil || p.ID == "" {
			return socket.Response{Error: "payload must be {\"id\":\"...\"}"}
		}
		if err := store.Revoke(p.ID); err != nil {
			return socket.Response{Error: err.Error()}
		}
		if err := store.SaveToFile(credsPath); err != nil {
			log.Warn("credential.revoke: save failed", "err", err)
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"revoked": true})}
	})

	_ = tlsCert // reserved for session management in a future version
}

// generateToken returns a "sghl_" prefixed token with 40 hex chars of entropy.
func generateToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sghl_" + hex.EncodeToString(b), nil
}

// --- Fleet socket handlers --------------------------------------------------

func registerFleetHandlers(srv *socket.Server, reporter *fleet.Reporter) {
	srv.Handle("fleet-preview", func(ctx context.Context, _ socket.Request) socket.Response {
		if reporter == nil {
			return socket.Response{Error: "fleet reporting is not enabled"}
		}
		report, err := reporter.Preview(ctx)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("fleet preview: %s", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(report)}
	})

	srv.Handle("fleet-opt-out", func(ctx context.Context, _ socket.Request) socket.Response {
		if reporter == nil {
			return socket.Response{Error: "fleet reporting is not enabled"}
		}
		reporter.OptOut()
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"ok": true})}
	})

	srv.Handle("fleet-policy", func(ctx context.Context, _ socket.Request) socket.Response {
		if reporter == nil {
			return socket.Response{Error: "fleet reporting is not enabled"}
		}
		policy := reporter.CurrentPolicy()
		if policy == nil {
			return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"policy": nil})}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(policy)}
	})
}
