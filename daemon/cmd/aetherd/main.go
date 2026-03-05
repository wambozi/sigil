// Command aetherd is the Aether OS daemon — a self-tuning intelligence layer
// that observes workflow patterns, builds a local user model, and reshapes
// the developer environment.
//
// Phase 1 (v0) capabilities:
//   - Collector: file events, process events, Hyprland IPC, git activity
//   - Store: SQLite (WAL mode) — all data stays local
//   - Analyzer: hourly heuristic pass + Cactus LLM summary
//   - Actuator: desktop notifications via notify-send
//   - Socket: Unix domain socket for aetherctl and the future shell
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wambozi/aether/internal/actuator"
	"github.com/wambozi/aether/internal/analyzer"
	"github.com/wambozi/aether/internal/cactus"
	"github.com/wambozi/aether/internal/collector"
	"github.com/wambozi/aether/internal/collector/sources"
	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/socket"
	"github.com/wambozi/aether/internal/store"
)

func main() {
	cfg := parseFlags()

	log := newLogger(cfg.logLevel)
	log.Info("aetherd starting", "version", "0.1.0-dev")

	if err := run(cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// --- Config -----------------------------------------------------------------

type config struct {
	dbPath       string
	socketPath   string
	cactusURL    string
	cactusModel  string
	cactusRoute  string
	watchPaths   []string
	repoPaths    []string
	analyzeEvery time.Duration
	logLevel     string
}

func parseFlags() config {
	var (
		dbPath       = flag.String("db", defaultDBPath(), "SQLite database path")
		socketPath   = flag.String("socket", defaultSocketPath(), "Unix socket path")
		cactusURL    = flag.String("cactus-url", "http://127.0.0.1:8080", "Cactus inference endpoint")
		cactusModel  = flag.String("cactus-model", "local", "Model name to request from Cactus")
		cactusRoute  = flag.String("cactus-route", "localfirst", "Cactus routing mode: local|localfirst|remotefirst|remote")
		watchPaths   = flag.String("watch", homeDir(), "Comma-separated directories to watch for file events")
		repoPaths    = flag.String("repos", homeDir(), "Comma-separated git repository roots to watch")
		analyzeEvery = flag.Duration("analyze-every", time.Hour, "How often to run an analysis cycle")
		logLevel     = flag.String("log-level", "info", "Log level: debug|info|warn|error")
	)
	flag.Parse()

	return config{
		dbPath:       *dbPath,
		socketPath:   *socketPath,
		cactusURL:    *cactusURL,
		cactusModel:  *cactusModel,
		cactusRoute:  *cactusRoute,
		watchPaths:   splitPaths(*watchPaths),
		repoPaths:    splitPaths(*repoPaths),
		analyzeEvery: *analyzeEvery,
		logLevel:     *logLevel,
	}
}

// --- Runtime ----------------------------------------------------------------

func run(cfg config, log *slog.Logger) error {
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

	// --- Cactus client ------------------------------------------------------
	cactusClient := cactus.New(cfg.cactusURL, cfg.cactusModel,
		cactus.RoutingMode(cfg.cactusRoute))

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := cactusClient.Ping(pingCtx); err != nil {
		log.Warn("cactus unreachable at startup — analysis will run local-only until it comes up",
			"url", cfg.cactusURL, "err", err)
		// Non-fatal: the daemon is still useful without Cactus.
		cactusClient = nil
	} else {
		log.Info("cactus reachable", "url", cfg.cactusURL, "routing", cfg.cactusRoute)
	}
	cancel()

	// --- Collector ----------------------------------------------------------
	terminalSrc := sources.NewTerminalSource()

	col := collector.New(db, log)
	col.Add(&sources.FileSource{Paths: cfg.watchPaths})
	col.Add(&sources.ProcessSource{})
	col.Add(&sources.GitSource{RepoPaths: cfg.repoPaths})
	col.Add(terminalSrc)

	if err := col.Start(ctx); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}
	log.Info("collector started", "sources", 5)

	// --- Actuator -----------------------------------------------------------
	act := actuator.New(log)

	// --- Analyzer -----------------------------------------------------------
	anlz := analyzer.New(db, cactusClient, cfg.analyzeEvery, log)
	anlz.OnSummary = act.OnSummary

	go anlz.Run(ctx)
	log.Info("analyzer started", "interval", cfg.analyzeEvery)

	// --- Socket server ------------------------------------------------------
	srv := socket.New(cfg.socketPath, log)
	registerHandlers(srv, db, terminalSrc, log)

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start socket: %w", err)
	}
	log.Info("socket listening", "path", cfg.socketPath)

	// --- Wait for shutdown --------------------------------------------------
	<-ctx.Done()
	log.Info("shutdown signal received")

	srv.Stop()
	col.Stop()

	log.Info("aetherd stopped cleanly")
	return nil
}

// --- Socket handlers --------------------------------------------------------

func registerHandlers(srv *socket.Server, db *store.Store, terminalSrc *sources.TerminalSource, log *slog.Logger) {
	// status — quick health check for aetherctl and the shell.
	srv.Handle("status", func(ctx context.Context, _ socket.Request) socket.Response {
		return socket.Response{
			OK:      true,
			Payload: socket.MarshalPayload(map[string]any{"status": "ok", "version": "0.1.0-dev"}),
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

	// ingest — called by the shell hook after each command.
	// Payload: {"cmd":"...","exit_code":0,"cwd":"/home/...","ts":1234567890}
	srv.Handle("ingest", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Cmd      string `json:"cmd"`
			ExitCode int    `json:"exit_code"`
			Cwd      string `json:"cwd"`
			Ts       int64  `json:"ts"`
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

		terminalSrc.Ingest(event.Event{
			Kind:   event.KindTerminal,
			Source: "terminal",
			Payload: map[string]any{
				"cmd":       p.Cmd,
				"exit_code": p.ExitCode,
				"cwd":       p.Cwd,
			},
			Timestamp: ts,
		})

		log.Debug("terminal event ingested", "cmd", p.Cmd, "exit_code", p.ExitCode, "cwd", p.Cwd)
		return socket.Response{OK: true}
	})
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
	return filepath.Join(base, "aetherd", "data.db")
}

func defaultSocketPath() string {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(runtime, "aetherd.sock")
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
