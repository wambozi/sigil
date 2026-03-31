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
	"github.com/wambozi/sigil/internal/mcp"
	"github.com/wambozi/sigil/internal/ml"
	"net/http"

	"github.com/wambozi/sigil/internal/network"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/plugin"
	"github.com/wambozi/sigil/internal/socket"
	siglogging "github.com/wambozi/sigil/internal/logging"
	"github.com/wambozi/sigil/internal/store"
	"github.com/wambozi/sigil/internal/task"
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
	applyTierDefaults(cfg.fileCfg)

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
	configPath    string         // path to the TOML config file on disk
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
		analyzeEvery  = flag.Duration("analyze-every", parseAnalyzeEvery(fileCfg), "How often to run an analysis cycle")
		notifierLevel = flag.Int("level", fileCfg.Notifier.LevelOrDefault(), "Notification level 0=silent 1=digest 2=ambient 3=conversational 4=autonomous")
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
		configPath:    *cfgPath,
		fileCfg:       fileCfg,
	}
}

// parseAnalyzeEvery returns the analyze interval from config, defaulting to 1h.
func parseAnalyzeEvery(cfg *config.Config) time.Duration {
	if cfg.Schedule.AnalyzeEvery == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(cfg.Schedule.AnalyzeEvery)
	if err != nil {
		return time.Hour
	}
	return d
}

// applyTierDefaults sets sensible defaults based on the cloud tier.
// Tiers set defaults, not hard limits — explicit user settings are preserved.
func applyTierDefaults(cfg *config.Config) {
	switch cfg.Cloud.Tier {
	case "", "free":
		// Free tier: all defaults are local (already the case).
	case "pro":
		if cfg.Inference.Mode == "" {
			cfg.Inference.Mode = "remotefirst"
		}
	case "team":
		if cfg.Inference.Mode == "" {
			cfg.Inference.Mode = "remotefirst"
		}
		if cfg.CloudSync.Enabled == nil {
			t := true
			cfg.CloudSync.Enabled = &t
		}
	default:
		slog.Warn("unrecognized cloud tier, using free-tier defaults", "tier", cfg.Cloud.Tier)
	}
}

// --- Runtime ----------------------------------------------------------------

func run(cfg daemonConfig, log *slog.Logger) error {
	startTime := time.Now()

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
			ModelName: cfg.fileCfg.Inference.Local.ModelName,
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
	addPlatformSources(col, log)

	if err := col.Start(ctx); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}
	log.Info("collector started")

	// --- Task Tracker -------------------------------------------------------
	taskTracker := task.NewTracker(db, log)
	if err := taskTracker.Restore(ctx); err != nil {
		log.Warn("task tracker: restore failed", "err", err)
	}
	go taskTracker.RunEventLoop(ctx, col.Subscribe())
	log.Info("task tracker started")

	// --- ML Engine ----------------------------------------------------------
	mlCfg := ml.Config{
		Mode:         ml.RoutingMode(cfg.fileCfg.ML.Mode),
		RetrainEvery: cfg.fileCfg.ML.RetrainEvery,
		Local: ml.LocalConfig{
			Enabled:   cfg.fileCfg.ML.Local.Enabled,
			ServerURL: cfg.fileCfg.ML.Local.ServerURL,
			ServerBin: cfg.fileCfg.ML.Local.ServerBin,
		},
		Cloud: ml.CloudConfig{
			Enabled: cfg.fileCfg.ML.Cloud.Enabled,
			BaseURL: cfg.fileCfg.ML.Cloud.BaseURL,
			APIKey:  cfg.fileCfg.ML.Cloud.APIKey,
		},
	}
	mlEngine, err := ml.New(mlCfg, log)
	if err != nil {
		log.Warn("ml engine: creation failed", "err", err)
		mlEngine = &ml.Engine{} // nil-safe disabled engine
	} else if mlEngine.Enabled() {
		pingCtx, cancelML := context.WithTimeout(ctx, 5*time.Second)
		if err := mlEngine.Ping(pingCtx); err != nil {
			log.Warn("ml engine unreachable at startup — will retry", "err", err)
		} else {
			log.Info("ml engine reachable")
		}
		cancelML()
	}
	defer mlEngine.Close()

	// Wire ML into the task tracker for predictions and retraining.
	if mlEngine.Enabled() {
		mlEngine.SetStore(db)
		taskTracker.SetMLEngine(&mlEngineAdapter{mlEngine}, cfg.dbPath, cfg.fileCfg.ML.RetrainEvery)
	}

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
	registerHandlers(srv, db, engine, ntf, anlz, terminalSrc, log, &currentRSSMB, nextDigest, &currentProfile, cfg, startTime, stop)
	registerTaskHandlers(srv, taskTracker, db)
	registerMLHandlers(srv, mlEngine, cfg.dbPath)
	registerInitHandlers(srv)
	registerAnalyticsHandlers(srv, db)
	registerTimelineHandlers(srv, db)
	registerCloudHandlers(srv, cfg)
	registerNotificationHandlers(srv, cfg)

	// --- MCP Tool Registry + Ask Handler ------------------------------------
	mcpRegistry := mcp.NewRegistry()
	mcp.RegisterStoreTools(mcpRegistry, db)

	srv.Handle("ask", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.Query == "" {
			return socket.Response{Error: "query is required"}
		}

		result, err := mcpRegistry.RunToolLoop(ctx, &mcpEngineAdapter{engine}, p.Query)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}

		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"answer":          result.Answer,
			"tool_calls_made": result.ToolCallsMade,
			"latency_ms":      result.TotalLatencyMS,
		})}
	})

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

	// If sigil-app (tray app) is installed, suppress osascript/notify-send
	// entirely — the tray app handles notifications natively with proper
	// icon ownership and click routing. Suggestions are still stored and
	// pushed via socket subscription.
	if _, err := exec.LookPath("sigil-app"); err == nil {
		ntf.SuppressPlatformNotifications = true
		log.Info("sigil-app detected in PATH; platform notifications suppressed")
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

	// Auto-test actuator: runs project tests after file saves at level 4.
	autoTest := actuator.NewAutoTestActuator(log,
		func() int { return int(ntf.Level()) },
		func(a actuator.Action) { reg.Notify(a) },
	)
	go autoTest.RunEventLoop(col.Subscribe())

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

	// --- Plugin ingest HTTP server ------------------------------------------
	pluginIngest := plugin.NewIngestServer(func(pe plugin.Event) {
		corr, _ := json.Marshal(pe.Correlation)
		payload, _ := json.Marshal(pe.Payload)
		if err := db.InsertPluginEvent(ctx, pe.Plugin, pe.Kind, string(corr), string(payload)); err != nil {
			log.Error("plugin ingest: store event", "plugin", pe.Plugin, "err", err)
		}
	}, log)
	pluginHTTP := &http.Server{
		Addr:    "127.0.0.1:7775",
		Handler: pluginIngest.Handler(),
	}
	go func() {
		if err := pluginHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("plugin ingest server", "err", err)
		}
	}()
	log.Info("plugin ingest listening", "addr", "127.0.0.1:7775")

	// --- Plugin Manager -----------------------------------------------------
	pluginMgr := plugin.NewManager("http://127.0.0.1:7775/api/v1/ingest", log)
	for name, pcfg := range cfg.fileCfg.Plugins {
		pluginMgr.Register(plugin.Config{
			Name:         name,
			Enabled:      pcfg.Enabled,
			Binary:       pcfg.Binary,
			Daemon:       pcfg.Daemon,
			PollInterval: pcfg.PollInterval,
			HealthURL:    pcfg.HealthURL,
			Env:          pcfg.Env,
		})
	}
	if err := pluginMgr.Start(ctx); err != nil {
		log.Warn("plugin manager: start failed", "err", err)
	}
	defer pluginMgr.Stop()

	// Wire capabilities provider into the HTTP ingest server so sigil-ml
	// can query GET /api/v1/capabilities to discover installed plugins.
	pluginIngest.SetCapabilitiesProvider(func() []plugin.Capabilities {
		var caps []plugin.Capabilities
		for _, s := range pluginMgr.Plugins() {
			if !s.Enabled {
				continue
			}
			c, err := plugin.DiscoverCapabilities(s.Name)
			if err != nil {
				continue
			}
			caps = append(caps, *c)
		}
		return caps
	})

	// Plugin status socket handler (registered after manager is created).
	srv.Handle("plugin-status", func(ctx context.Context, _ socket.Request) socket.Response {
		return socket.Response{OK: true, Payload: socket.MarshalPayload(pluginMgr.Plugins())}
	})

	srv.Handle("plugin-capabilities", func(ctx context.Context, _ socket.Request) socket.Response {
		type pluginCaps struct {
			Name        string                `json:"name"`
			Enabled     bool                  `json:"enabled"`
			Running     bool                  `json:"running"`
			Healthy     bool                  `json:"healthy"`
			Actions     []plugin.PluginAction `json:"actions,omitempty"`
			DataSources []string              `json:"data_sources,omitempty"`
		}
		var result []pluginCaps
		for _, s := range pluginMgr.Plugins() {
			pc := pluginCaps{Name: s.Name, Enabled: s.Enabled, Running: s.Running, Healthy: s.Healthy}
			if s.Enabled {
				caps, err := plugin.DiscoverCapabilities(s.Name)
				if err == nil && caps != nil {
					pc.Actions = caps.Actions
					pc.DataSources = caps.DataSources
				}
			}
			result = append(result, pc)
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(result)}
	})

	// set-config — update the config file on disk.
	// Payload: a partial config.Config JSON object.
	// API-key fields that start with "****" are preserved from the existing file.
	srv.Handle("set-config", func(ctx context.Context, req socket.Request) socket.Response {
		// Parse the incoming config.
		var incoming config.Config
		if err := json.Unmarshal(req.Payload, &incoming); err != nil {
			return socket.Response{Error: "invalid config payload: " + err.Error()}
		}

		// Load the current config from disk so we can detect changes and
		// preserve masked API keys.
		current, err := config.Load(cfg.configPath)
		if err != nil {
			return socket.Response{Error: "load current config: " + err.Error()}
		}

		// Preserve masked API keys: if the incoming value starts with "****",
		// keep the old value.
		if strings.HasPrefix(incoming.Inference.Cloud.APIKey, "****") {
			incoming.Inference.Cloud.APIKey = current.Inference.Cloud.APIKey
		}
		if strings.HasPrefix(incoming.ML.Cloud.APIKey, "****") {
			incoming.ML.Cloud.APIKey = current.ML.Cloud.APIKey
		}

		// Detect restart-requiring field changes before merging.
		restartRequired := false
		if len(incoming.Daemon.WatchDirs) > 0 && fmt.Sprint(incoming.Daemon.WatchDirs) != fmt.Sprint(current.Daemon.WatchDirs) {
			restartRequired = true
		}
		if incoming.Inference.Mode != "" && incoming.Inference.Mode != current.Inference.Mode {
			restartRequired = true
		}
		if incoming.Network.Enabled != current.Network.Enabled {
			restartRequired = true
		}
		if incoming.Network.Bind != "" && incoming.Network.Bind != current.Network.Bind {
			restartRequired = true
		}
		if incoming.Network.Port != 0 && incoming.Network.Port != current.Network.Port {
			restartRequired = true
		}

		// Merge incoming over current and save.
		merged := current
		// Re-use the config package's internal merge by loading the incoming
		// as an overlay. We serialize incoming → TOML → reload to reuse merge.
		// For simplicity, just do a direct field merge here.
		if incoming.Daemon.LogLevel != "" {
			merged.Daemon.LogLevel = incoming.Daemon.LogLevel
		}
		if len(incoming.Daemon.WatchDirs) > 0 {
			merged.Daemon.WatchDirs = incoming.Daemon.WatchDirs
		}
		if len(incoming.Daemon.RepoDirs) > 0 {
			merged.Daemon.RepoDirs = incoming.Daemon.RepoDirs
		}
		if incoming.Daemon.DBPath != "" {
			merged.Daemon.DBPath = incoming.Daemon.DBPath
		}
		if incoming.Daemon.SocketPath != "" {
			merged.Daemon.SocketPath = incoming.Daemon.SocketPath
		}
		if incoming.Daemon.ActuationsEnabled != nil {
			merged.Daemon.ActuationsEnabled = incoming.Daemon.ActuationsEnabled
		}
		if incoming.Notifier.Level != nil {
			merged.Notifier.Level = incoming.Notifier.Level
		}
		if incoming.Notifier.DigestTime != "" {
			merged.Notifier.DigestTime = incoming.Notifier.DigestTime
		}
		if incoming.Inference.Mode != "" {
			merged.Inference.Mode = incoming.Inference.Mode
		}
		merged.Inference.Local.Enabled = incoming.Inference.Local.Enabled
		if incoming.Inference.Local.ServerURL != "" {
			merged.Inference.Local.ServerURL = incoming.Inference.Local.ServerURL
		}
		merged.Inference.Cloud.Enabled = incoming.Inference.Cloud.Enabled
		if incoming.Inference.Cloud.APIKey != "" {
			merged.Inference.Cloud.APIKey = incoming.Inference.Cloud.APIKey
		}
		if incoming.Inference.Cloud.Provider != "" {
			merged.Inference.Cloud.Provider = incoming.Inference.Cloud.Provider
		}
		if incoming.Inference.Cloud.BaseURL != "" {
			merged.Inference.Cloud.BaseURL = incoming.Inference.Cloud.BaseURL
		}
		if incoming.Inference.Cloud.Model != "" {
			merged.Inference.Cloud.Model = incoming.Inference.Cloud.Model
		}
		if incoming.Retention.RawEventDays != 0 {
			merged.Retention.RawEventDays = incoming.Retention.RawEventDays
		}
		if incoming.ML.Mode != "" {
			merged.ML.Mode = incoming.ML.Mode
		}
		merged.ML.Local.Enabled = incoming.ML.Local.Enabled
		if incoming.ML.Local.ServerURL != "" {
			merged.ML.Local.ServerURL = incoming.ML.Local.ServerURL
		}
		merged.ML.Cloud.Enabled = incoming.ML.Cloud.Enabled
		if incoming.ML.Cloud.APIKey != "" {
			merged.ML.Cloud.APIKey = incoming.ML.Cloud.APIKey
		}
		// Fleet: always copy Enabled since the frontend sends the full
		// fleet object. A zero-value Endpoint means "keep existing".
		merged.Fleet.Enabled = incoming.Fleet.Enabled
		if incoming.Fleet.Endpoint != "" {
			merged.Fleet.Endpoint = incoming.Fleet.Endpoint
		}
		if len(incoming.Plugins) > 0 {
			if merged.Plugins == nil {
				merged.Plugins = make(map[string]config.PluginConfig)
			}
			for k, v := range incoming.Plugins {
				merged.Plugins[k] = v
			}
		}
		if incoming.Network.Enabled {
			merged.Network.Enabled = true
		}
		if incoming.Network.Bind != "" {
			merged.Network.Bind = incoming.Network.Bind
		}
		if incoming.Network.Port != 0 {
			merged.Network.Port = incoming.Network.Port
		}

		if err := config.Save(cfg.configPath, merged); err != nil {
			return socket.Response{Error: "save config: " + err.Error()}
		}

		// Update the in-memory fileCfg reference.
		cfg.fileCfg = merged

		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"saved":            true,
			"restart_required": restartRequired,
		})}
	})

	// plugin-registry — return all available plugins from the built-in registry.
	srv.Handle("plugin-registry", func(ctx context.Context, _ socket.Request) socket.Response {
		entries := plugin.Registry()
		type registryRow struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Category    string `json:"category"`
			Binary      string `json:"binary"`
			Installed   bool   `json:"installed"`
		}
		rows := make([]registryRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, registryRow{
				Name:        e.Name,
				Description: e.Description,
				Version:     e.Version,
				Category:    e.Category,
				Binary:      e.Binary,
				Installed:   plugin.IsInstalled(e.Name),
			})
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(rows)}
	})

	// plugin-install — install a plugin by name.
	srv.Handle("plugin-install", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.Name == "" {
			return socket.Response{Error: "name is required"}
		}
		entry := plugin.Lookup(p.Name)
		if entry == nil {
			return socket.Response{Error: fmt.Sprintf("unknown plugin %q", p.Name)}
		}
		if err := plugin.Install(p.Name, plugin.DetectInstallMethod()); err != nil {
			return socket.Response{Error: fmt.Sprintf("install %s: %s", p.Name, err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"installed": true,
		})}
	})

	// plugin-enable — enable a plugin by name.
	srv.Handle("plugin-enable", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.Name == "" {
			return socket.Response{Error: "name is required"}
		}
		if err := pluginMgr.Enable(ctx, p.Name); err != nil {
			return socket.Response{Error: fmt.Sprintf("enable %s: %s", p.Name, err)}
		}
		// Update config on disk.
		if cfg.fileCfg.Plugins == nil {
			cfg.fileCfg.Plugins = make(map[string]config.PluginConfig)
		}
		pc := cfg.fileCfg.Plugins[p.Name]
		pc.Enabled = true
		cfg.fileCfg.Plugins[p.Name] = pc
		if err := config.Save(cfg.configPath, cfg.fileCfg); err != nil {
			log.Warn("plugin-enable: save config", "err", err)
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"enabled": true,
		})}
	})

	// plugin-disable — disable a plugin by name.
	srv.Handle("plugin-disable", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.Name == "" {
			return socket.Response{Error: "name is required"}
		}
		if err := pluginMgr.Disable(p.Name); err != nil {
			return socket.Response{Error: fmt.Sprintf("disable %s: %s", p.Name, err)}
		}
		// Update config on disk.
		if cfg.fileCfg.Plugins == nil {
			cfg.fileCfg.Plugins = make(map[string]config.PluginConfig)
		}
		pc := cfg.fileCfg.Plugins[p.Name]
		pc.Enabled = false
		cfg.fileCfg.Plugins[p.Name] = pc
		if err := config.Save(cfg.configPath, cfg.fileCfg); err != nil {
			log.Warn("plugin-disable: save config", "err", err)
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"enabled": false,
		})}
	})

	// Register plugin-aware MCP tools now that the manager exists.
	mcp.RegisterPluginTools(mcpRegistry, pluginMgr, log)

	// --- Task transition → LLM suggestion → plugin action ------------------
	taskTracker.OnTransition = func(oldPhase, newPhase task.Phase, t *task.Task) {
		// Only act on significant transitions.
		if newPhase != task.PhaseIdle && newPhase != task.PhaseStuck {
			return
		}

		level := ntf.Level()

		// Level 0-1: silent, no LLM call.
		if level < notifier.LevelAmbient {
			return
		}

		// Check workflow state — respect flow state before interrupting.
		ctx := context.Background()
		wsPred, _ := db.QueryLatestPrediction(ctx, "suggest")
		if wsPred != nil {
			dominantState, _ := wsPred.Result["dominant_state"].(string)
			focusScore, _ := wsPred.Result["focus_score"].(float64)

			// Don't interrupt deep work with high focus.
			if dominantState == "deep_work" && focusScore > 0.8 {
				log.Info("transition suggestion: skipping — engineer in deep work",
					"focus_score", focusScore)
				return
			}
		}

		// Build a query based on the transition.
		var query string
		confidence := notifier.ConfidenceModerate
		switch {
		case oldPhase != task.PhaseIdle && newPhase == task.PhaseIdle:
			query = fmt.Sprintf(
				"The engineer just completed a task on branch '%s' in %s. "+
					"What should they work on next? Check their sprint backlog, open PRs, and recent task history. "+
					"Be concise — one paragraph max.",
				t.Branch, filepath.Base(t.RepoRoot))
		case newPhase == task.PhaseStuck:
			query = fmt.Sprintf(
				"The engineer is stuck on branch '%s' in %s — %d consecutive test failures. "+
					"What should they try? Check the recent errors and suggest a different approach. "+
					"Be concise.",
				t.Branch, filepath.Base(t.RepoRoot), t.TestFailures)

			// Elevate urgency when blocked with negative momentum.
			if wsPred != nil {
				momentum, _ := wsPred.Result["momentum"].(float64)
				if momentum < -0.5 {
					confidence = notifier.ConfidenceStrong
				}
			}
		default:
			return
		}

		// Ask the LLM with MCP tools.
		result, err := mcpRegistry.RunToolLoop(ctx, &mcpEngineAdapter{engine}, query)
		if err != nil {
			log.Warn("transition suggestion: LLM failed", "err", err)
			return
		}

		suggestion := notifier.Suggestion{
			Category:   "task_transition",
			Confidence: confidence,
			Title:      "Sigil",
			Body:       result.Answer,
		}

		// Level 3+: check if claude plugin can launch a session.
		if level >= notifier.LevelConversational {
			suggestion.ActionCmd = fmt.Sprintf(
				`sigil-plugin-claude launch --prompt %q --cwd %q`,
				result.Answer, t.RepoRoot)
		}

		ntf.Surface(suggestion)
		log.Info("transition suggestion surfaced",
			"phase", newPhase, "tools", result.ToolCallsMade, "level", level)
	}

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

// countEventsToday returns the number of events recorded since midnight UTC today.
// Errors are logged and a zero count is returned so the status handler stays healthy.
func countEventsToday(ctx context.Context, db *store.Store, log *slog.Logger) int64 {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	n, err := db.CountEvents(ctx, "", midnight)
	if err != nil {
		log.Warn("countEventsToday: query failed", "err", err)
		return 0
	}
	return n
}

// summarizeEvent produces a short human-readable summary from an event's payload.
func summarizeEvent(e event.Event) string {
	switch e.Kind {
	case event.KindTerminal:
		if cmd, ok := e.Payload["cmd"].(string); ok {
			return cmd
		}
	case event.KindFile:
		if path, ok := e.Payload["path"].(string); ok {
			return path
		}
	case event.KindGit:
		if msg, ok := e.Payload["message"].(string); ok {
			return msg
		}
		if action, ok := e.Payload["action"].(string); ok {
			return action
		}
	case event.KindProcess:
		if name, ok := e.Payload["name"].(string); ok {
			return name
		}
	case event.KindHyprland:
		if title, ok := e.Payload["title"].(string); ok {
			return title
		}
	}
	return string(e.Kind) + " event"
}

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
	startTime time.Time,
	cancelFunc context.CancelFunc,
) {
	// status — quick health check for sigilctl and the shell.
	srv.Handle("status", func(ctx context.Context, _ socket.Request) socket.Response {
		payload := map[string]any{
			"status":                     "ok",
			"version":                    "0.1.0-dev",
			"notifier_level":             int(ntf.Level()),
			"rss_mb":                     rssMB.Load(),
			"current_keybinding_profile": currentProfile.Load(),
			"uptime_seconds":             int64(time.Since(startTime).Seconds()),
			"events_today":               countEventsToday(ctx, db, log),
			"analysis_interval":          cfg.analyzeEvery.String(),
			"routing_mode":               cfg.inferenceMode,
			"active_sources":             []string{"file", "process", "git", "terminal", "hyprland"},
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

	// metrics — process-level resource metrics for the panel.
	srv.Handle("metrics", func(_ context.Context, _ socket.Request) socket.Response {
		pid := os.Getpid()
		sigildRSS, _ := socket.ProcRSS(pid)
		result := map[string]any{
			"sigild_pid":       pid,
			"sigild_rss_bytes": sigildRSS,
		}

		llamaPID, managed, ok := engine.LocalProcessInfo()
		llamaInfo := map[string]any{
			"active":  ok,
			"managed": managed,
		}
		if ok {
			llamaInfo["pid"] = llamaPID
			if rss, err := socket.ProcRSS(llamaPID); err == nil {
				llamaInfo["rss_bytes"] = rss
			}
			if cpu, err := socket.ProcCPUPercent(llamaPID); err == nil {
				llamaInfo["cpu_pct"] = cpu
			}
			llamaInfo["model_name"] = engine.LocalModelName()
			llamaInfo["context_tokens_max"] = engine.LocalCtxSize()
			// context_tokens_used would require querying llama-server /slots;
			// omitted for now — the panel can show max only.
			llamaInfo["context_tokens_used"] = 0
		}
		result["llama_server"] = llamaInfo
		return socket.Response{OK: true, Payload: socket.MarshalPayload(result)}
	})

	// events — return events from the store with optional pagination and filtering.
	// When called with no payload, returns recent 50 (backward compatible).
	// shutdown — graceful daemon shutdown triggered by sigilctl stop.
	srv.Handle("shutdown", func(_ context.Context, _ socket.Request) socket.Response {
		log.Info("shutdown requested via socket")
		go cancelFunc() // cancel in a goroutine so the response is sent first
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]string{"status": "shutting_down"})}
	})

	// events — return recent events from the store.
	srv.Handle("events", func(ctx context.Context, req socket.Request) socket.Response {
		if len(req.Payload) == 0 || string(req.Payload) == "null" {
			// Backward compatible: return recent 50.
			events, err := db.QueryEvents(ctx, "", 50)
			if err != nil {
				return socket.Response{Error: err.Error()}
			}
			return socket.Response{OK: true, Payload: socket.MarshalPayload(events)}
		}

		var params struct {
			Source string `json:"source"`
			After  int64  `json:"after"`
			Before int64  `json:"before"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := json.Unmarshal(req.Payload, &params); err != nil {
			return socket.Response{Error: fmt.Sprintf("events: invalid payload: %s", err)}
		}

		filter := store.EventFilter{
			Kind:   event.Kind(params.Source),
			After:  params.After,
			Before: params.Before,
			Limit:  params.Limit,
			Offset: params.Offset,
		}

		events, total, err := db.QueryEventsPaginated(ctx, filter)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}

		type eventSummary struct {
			ID         int64      `json:"id"`
			Kind       event.Kind `json:"kind"`
			Source     string     `json:"source"`
			Summary    string     `json:"summary"`
			Timestamp  int64      `json:"timestamp"`
			HasDetails bool       `json:"has_details"`
		}
		summaries := make([]eventSummary, 0, len(events))
		for _, e := range events {
			summary := summarizeEvent(e)
			summaries = append(summaries, eventSummary{
				ID:         e.ID,
				Kind:       e.Kind,
				Source:     e.Source,
				Summary:    summary,
				Timestamp:  e.Timestamp.UnixMilli(),
				HasDetails: len(e.Payload) > 0,
			})
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"events": summaries,
			"total":  total,
			"limit":  filter.Limit,
			"offset": filter.Offset,
		})}
	})

	// event-detail — return a single event with full payload.
	srv.Handle("event-detail", func(ctx context.Context, req socket.Request) socket.Response {
		var params struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(req.Payload, &params); err != nil {
			return socket.Response{Error: fmt.Sprintf("event-detail: invalid payload: %s", err)}
		}
		e, err := db.QueryEventByID(ctx, params.ID)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("event-detail: %s", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"id":        e.ID,
			"kind":      e.Kind,
			"source":    e.Source,
			"payload":   e.Payload,
			"timestamp": e.Timestamp.UnixMilli(),
		})}
	})

	// purge-events — selectively delete events by IDs or filter.
	srv.Handle("purge-events", func(ctx context.Context, req socket.Request) socket.Response {
		var params struct {
			IDs    []int64 `json:"ids"`
			Source string  `json:"source"`
			Before int64   `json:"before"`
			After  int64   `json:"after"`
		}
		if err := json.Unmarshal(req.Payload, &params); err != nil {
			return socket.Response{Error: fmt.Sprintf("purge-events: invalid payload: %s", err)}
		}

		var deleted int
		var err error
		if len(params.IDs) > 0 {
			deleted, err = db.DeleteEvents(ctx, params.IDs)
			log.Info("events purged by IDs", "count", deleted, "ids_requested", len(params.IDs))
		} else {
			filter := store.EventFilter{
				Kind:   event.Kind(params.Source),
				Before: params.Before,
				After:  params.After,
			}
			deleted, err = db.DeleteEventsFiltered(ctx, filter)
			log.Info("events purged by filter", "count", deleted, "source", params.Source, "before", params.Before, "after", params.After)
		}
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("purge-events: %s", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"deleted": deleted})}
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

		// Write ml_feedback event for sigil-ml learning loop.
		accepted := p.Outcome == "accepted"
		feedbackPayload := map[string]any{
			"model":         "suggest",
			"accepted":      accepted,
			"suggestion_id": p.SuggestionID,
		}
		// Include current workflow state if available.
		if wsPred, wsErr := db.QueryLatestPrediction(ctx, "suggest"); wsErr == nil && wsPred != nil {
			if state, ok := wsPred.Result["dominant_state"].(string); ok {
				feedbackPayload["state"] = state
			}
		}
		_ = db.InsertEvent(ctx, event.Event{
			Kind:      "ml_feedback",
			Source:    "notifier",
			Payload:   feedbackPayload,
			Timestamp: time.Now(),
		})

		log.Info("feedback recorded", "suggestion_id", p.SuggestionID, "outcome", p.Outcome)
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"ok": true})}
	})

	// correct — write an ml_correction event for a misclassified event.
	// Payload: {"event_id": 12345, "correct_category": "researching"}
	srv.Handle("correct", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			EventID         int64  `json:"event_id"`
			CorrectCategory string `json:"correct_category"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		if p.EventID <= 0 {
			return socket.Response{Error: "event_id must be a positive integer"}
		}
		validCategories := map[string]bool{
			"creating": true, "refining": true, "verifying": true, "navigating": true,
			"researching": true, "integrating": true, "communicating": true, "idle": true,
		}
		if !validCategories[p.CorrectCategory] {
			return socket.Response{Error: fmt.Sprintf("invalid category %q — valid: creating, refining, verifying, navigating, researching, integrating, communicating, idle", p.CorrectCategory)}
		}

		err := db.InsertEvent(ctx, event.Event{
			Kind:   "ml_correction",
			Source: "sigilctl",
			Payload: map[string]any{
				"event_id":         p.EventID,
				"correct_category": p.CorrectCategory,
			},
			Timestamp: time.Now(),
		})
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("insert correction: %s", err)}
		}
		log.Info("correction recorded", "event_id", p.EventID, "category", p.CorrectCategory)
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{"ok": true})}
	})

	// config — return the current configuration as JSON.
	// Loads from disk to pick up any changes made by set-config.
	// Sensitive fields (API keys / tokens) are masked.
	srv.Handle("config", func(ctx context.Context, _ socket.Request) socket.Response {
		snapshot, err := config.Load(cfg.configPath)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("load config: %v", err)}
		}

		// Apply runtime overrides from flags for fields not in the file.
		if snapshot.Daemon.LogLevel == "" {
			snapshot.Daemon.LogLevel = cfg.logLevel
		}
		if len(snapshot.Daemon.WatchDirs) == 0 {
			snapshot.Daemon.WatchDirs = cfg.watchPaths
		}
		if len(snapshot.Daemon.RepoDirs) == 0 {
			snapshot.Daemon.RepoDirs = cfg.repoPaths
		}

		// Mask sensitive fields.
		if snapshot.Inference.Cloud.APIKey != "" {
			snapshot.Inference.Cloud.APIKey = "****"
		}
		if snapshot.ML.Cloud.APIKey != "" {
			snapshot.ML.Cloud.APIKey = "****"
		}
		if snapshot.Cloud.APIKey != "" {
			snapshot.Cloud.APIKey = "****"
		}

		return socket.Response{OK: true, Payload: socket.MarshalPayload(snapshot)}
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

// --- Task socket handlers ---------------------------------------------------

func registerTaskHandlers(srv *socket.Server, tracker *task.Tracker, db *store.Store) {
	// task — return the current inferred task.
	srv.Handle("task", func(ctx context.Context, _ socket.Request) socket.Response {
		cur := tracker.Current()
		if cur == nil {
			return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
				"phase": "idle",
			})}
		}

		// Build top files list (sorted by edit count).
		type fileEntry struct {
			Path  string `json:"path"`
			Edits int    `json:"edits"`
		}
		files := make([]fileEntry, 0, len(cur.FilesTouched))
		for p, n := range cur.FilesTouched {
			files = append(files, fileEntry{p, n})
		}
		for i := 1; i < len(files); i++ {
			for j := i; j > 0 && files[j].Edits > files[j-1].Edits; j-- {
				files[j], files[j-1] = files[j-1], files[j]
			}
		}
		if len(files) > 10 {
			files = files[:10]
		}

		elapsed := time.Since(cur.StartedAt)

		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"id":            cur.ID,
			"phase":         string(cur.Phase),
			"repo_root":     cur.RepoRoot,
			"branch":        cur.Branch,
			"files":         files,
			"started_at":    cur.StartedAt.Format(time.RFC3339),
			"elapsed_min":   int(elapsed.Minutes()),
			"commit_count":  cur.CommitCount,
			"test_runs":     cur.TestRuns,
			"test_failures": cur.TestFailures,
		})}
	})

	// task-history — return recent task transitions.
	srv.Handle("task-history", func(ctx context.Context, _ socket.Request) socket.Response {
		since := time.Now().Add(-7 * 24 * time.Hour)
		tasks, err := db.QueryTaskHistory(ctx, since, 20)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		type row struct {
			ID        string `json:"id"`
			Phase     string `json:"phase"`
			RepoRoot  string `json:"repo_root"`
			Branch    string `json:"branch"`
			StartedAt string `json:"started_at"`
			Commits   int    `json:"commits"`
			Files     int    `json:"files"`
		}
		rows := make([]row, 0, len(tasks))
		for _, t := range tasks {
			rows = append(rows, row{
				ID:        t.ID,
				Phase:     t.Phase,
				RepoRoot:  t.RepoRoot,
				Branch:    t.Branch,
				StartedAt: t.StartedAt.Format(time.RFC3339),
				Commits:   t.CommitCount,
				Files:     len(t.Files),
			})
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(rows)}
	})

	// day-summary — return today's work summary with per-task breakdown.
	srv.Handle("day-summary", func(ctx context.Context, _ socket.Request) socket.Response {
		today := time.Now()
		tasks, err := db.QueryTasksByDate(ctx, today)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}

		repos := make(map[string]struct{})
		var totalCommits, completed, started int
		allFiles := make(map[string]struct{})
		var totalEditMin, totalVerifyMin, totalStuckMin float64

		// Per-task breakdown and speed score accumulation.
		type taskSummary struct {
			Branch      string  `json:"branch"`
			RepoRoot    string  `json:"repo_root"`
			Phase       string  `json:"phase"`
			DurationMin int     `json:"duration_min"`
			Files       int     `json:"files"`
			TotalEdits  int     `json:"total_edits"`
			Commits     int     `json:"commits"`
			TestRuns    int     `json:"test_runs"`
			TestFails   int     `json:"test_failures"`
			Completed   bool    `json:"completed"`
			SpeedScore  float64 `json:"speed_score"` // size-weighted velocity
		}
		var taskList []taskSummary
		var speedScoreSum, speedWeightSum float64

		for _, t := range tasks {
			started++
			if t.RepoRoot != "" {
				repos[t.RepoRoot] = struct{}{}
			}
			totalCommits += t.CommitCount
			isCompleted := t.CompletedAt != nil
			if isCompleted {
				completed++
			}
			totalEdits := 0
			for f, n := range t.Files {
				allFiles[f] = struct{}{}
				totalEdits += n
			}

			duration := t.LastActivity.Sub(t.StartedAt).Minutes()
			switch t.Phase {
			case "verifying":
				totalVerifyMin += duration
			case "stuck":
				totalStuckMin += duration
			default:
				totalEditMin += duration
			}

			// Speed score: edits-per-minute, weighted by task size (file count).
			// Only scored for completed tasks with meaningful duration.
			var score float64
			if isCompleted && duration > 1 && len(t.Files) > 0 {
				score = float64(totalEdits) / duration // edits per minute
				weight := float64(len(t.Files))
				speedScoreSum += score * weight
				speedWeightSum += weight
			}

			taskList = append(taskList, taskSummary{
				Branch:      t.Branch,
				RepoRoot:    t.RepoRoot,
				Phase:       t.Phase,
				DurationMin: int(duration),
				Files:       len(t.Files),
				TotalEdits:  totalEdits,
				Commits:     t.CommitCount,
				TestRuns:    t.TestRuns,
				TestFails:   t.TestFailures,
				Completed:   isCompleted,
				SpeedScore:  score,
			})
		}

		repoList := make([]string, 0, len(repos))
		for r := range repos {
			repoList = append(repoList, r)
		}

		// Aggregate speed score (weighted average across completed tasks).
		var daySpeedScore float64
		if speedWeightSum > 0 {
			daySpeedScore = speedScoreSum / speedWeightSum
		}

		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"date":              today.Format("2006-01-02"),
			"repos":             repoList,
			"tasks_started":     started,
			"tasks_completed":   completed,
			"total_commits":     totalCommits,
			"files_touched":     len(allFiles),
			"editing_minutes":   int(totalEditMin),
			"verifying_minutes": int(totalVerifyMin),
			"stuck_minutes":     int(totalStuckMin),
			"tasks":             taskList,
			"speed_score":       daySpeedScore,
		})}
	})
}

// --- ML socket handlers -----------------------------------------------------

func registerMLHandlers(srv *socket.Server, engine *ml.Engine, dbPath string) {
	// ml-status — health and loaded models.
	srv.Handle("ml-status", func(ctx context.Context, _ socket.Request) socket.Response {
		if !engine.Enabled() {
			return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
				"status": "disabled",
			})}
		}
		err := engine.Ping(ctx)
		status := "ok"
		if err != nil {
			status = "unreachable"
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"status": status,
		})}
	})

	// ml-train — trigger retraining.
	srv.Handle("ml-train", func(ctx context.Context, _ socket.Request) socket.Response {
		if !engine.Enabled() {
			return socket.Response{Error: "ml engine is disabled"}
		}
		result, err := engine.Train(ctx, dbPath)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(result)}
	})

	// ml-predict — run all predictions for current state.
	srv.Handle("ml-predict", func(ctx context.Context, req socket.Request) socket.Response {
		if !engine.Enabled() {
			return socket.Response{Error: "ml engine is disabled"}
		}
		var p struct {
			Endpoint string         `json:"endpoint"`
			Features map[string]any `json:"features"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: "invalid payload: " + err.Error()}
		}
		pred, err := engine.Predict(ctx, p.Endpoint, p.Features)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(pred)}
	})
}

// --- MCP engine adapter ----------------------------------------------------

// mcpEngineAdapter wraps inference.Engine to satisfy mcp.ToolEngine.
type mcpEngineAdapter struct {
	engine *inference.Engine
}

func (a *mcpEngineAdapter) CompleteWithTools(ctx context.Context, messages []mcp.Message, tools []mcp.ToolDef) (*mcp.ToolEngineResult, error) {
	chatMsgs := make([]inference.ChatMessage, len(messages))
	for i, m := range messages {
		chatMsgs[i] = inference.ChatMessage{
			Role: m.Role, Content: m.Content,
			ToolCallID: m.ToolCallID, Name: m.Name,
		}
		for _, tc := range m.ToolCalls {
			chatMsgs[i].ToolCalls = append(chatMsgs[i].ToolCalls, inference.ChatToolCall{
				ID: tc.ID, Type: tc.Type,
				Function: inference.ChatToolCallFunc{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
	}
	chatTools := make([]inference.ChatToolDef, len(tools))
	for i, t := range tools {
		chatTools[i] = inference.ChatToolDef{
			Type: t.Type,
			Function: inference.ChatToolDefFunc{
				Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			},
		}
	}
	result, err := a.engine.CompleteWithTools(ctx, chatMsgs, chatTools)
	if err != nil {
		return nil, err
	}
	out := &mcp.ToolEngineResult{Content: result.Content, Routing: result.Routing, LatencyMS: result.LatencyMS}
	for _, tc := range result.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, mcp.ToolCall{
			ID: tc.ID, Type: tc.Type,
			Function: mcp.ToolCallFunc{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		})
	}
	return out, nil
}

// --- ML adapter for task tracker -------------------------------------------

// mlEngineAdapter wraps ml.Engine to satisfy task.MLPredictor.
type mlEngineAdapter struct {
	engine *ml.Engine
}

func (a *mlEngineAdapter) Predict(ctx context.Context, endpoint string, features map[string]any) (map[string]any, error) {
	pred, err := a.engine.Predict(ctx, endpoint, features)
	if err != nil {
		return nil, err
	}
	return pred.Result, nil
}

func (a *mlEngineAdapter) Train(ctx context.Context, dbPath string) error {
	_, err := a.engine.Train(ctx, dbPath)
	return err
}

func (a *mlEngineAdapter) Enabled() bool {
	return a.engine.Enabled()
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
	return siglogging.New("sigild", level)
}

func defaultDBPath() string {
	if goruntime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild", "data.db")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".local", "share")
	}
	return filepath.Join(base, "sigild", "data.db")
}

func defaultSocketPath() string {
	if goruntime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild.sock")
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sigild.sock")
	}
	// Linux: /run/user/<uid> is the conventional runtime dir.
	// macOS: /run doesn't exist — use os.TempDir() which returns
	// the per-user $TMPDIR (e.g. /var/folders/xx/.../T/).
	if goruntime.GOOS == "darwin" {
		return filepath.Join(os.TempDir(), "sigild.sock")
	}
	return fmt.Sprintf("/run/user/%d/sigild.sock", currentUID())
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
	if goruntime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil")
	}
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
