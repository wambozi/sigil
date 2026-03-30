package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/wambozi/sigil/internal/socket"
)

// App is the Wails-bound backend. It communicates with sigild over the Unix
// domain socket using the same newline-delimited JSON protocol as sigilctl and
// the VS Code extension. Short-lived connections are used for RPC calls; a
// persistent connection handles push subscriptions.
type App struct {
	ctx        context.Context
	socketPath string
	connected  bool
	mu         sync.RWMutex

	// Persistent subscription connection.
	subConn   net.Conn
	subCancel context.CancelFunc

	notifier Notifier
	log      *slog.Logger

	// Auto-update state.
	update updateState
}

// NewApp returns an App with sensible defaults. Wails will call startup() and
// shutdown() at the appropriate lifecycle points.
func NewApp() *App {
	return &App{
		log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// startup is called by Wails when the application starts. It discovers the
// socket path, initialises the notifier, and starts the subscription goroutine.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.socketPath = defaultSocketPath()
	a.notifier = newNotifier()
	a.log.Info("sigil-app starting", "socket", a.socketPath)

	subCtx, cancel := context.WithCancel(ctx)
	a.subCancel = cancel
	go a.startSubscription(subCtx)

	go setupTray(a)
	go a.checkUpdateOnStartup()
}

// shutdown is called by Wails when the application is shutting down.
func (a *App) shutdown(ctx context.Context) {
	if a.subCancel != nil {
		a.subCancel()
	}
	a.mu.Lock()
	if a.subConn != nil {
		a.subConn.Close()
	}
	a.mu.Unlock()
	if a.notifier != nil {
		a.notifier.Close()
	}
}

// ---------------------------------------------------------------------------
// Socket RPC — adapted from cmd/sigilctl/main.go:469
// ---------------------------------------------------------------------------

// call sends a single JSON-over-Unix-socket RPC request to sigild and returns
// the response. Each call opens a new short-lived connection (same pattern as
// sigilctl).
func (a *App) call(method string, payload any) (socket.Response, error) {
	conn, err := net.Dial("unix", a.socketPath)
	if err != nil {
		return socket.Response{}, fmt.Errorf("connect to daemon at %s: %w", a.socketPath, err)
	}
	defer conn.Close()

	req := socket.Request{Method: method}
	if payload != nil {
		req.Payload, _ = json.Marshal(payload)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return socket.Response{}, fmt.Errorf("send request: %w", err)
	}

	var resp socket.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return socket.Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Socket path discovery — adapted from cmd/sigilctl/main.go:539
// ---------------------------------------------------------------------------

func defaultSocketPath() string {
	switch goruntime.GOOS {
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(localApp, "sigil", "sigild.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "sigild.sock")
	default: // linux and others
		if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
			return filepath.Join(dir, "sigild.sock")
		}
		return fmt.Sprintf("/run/user/%d/sigild.sock", os.Getuid())
	}
}

// ---------------------------------------------------------------------------
// Push subscription with exponential backoff
// ---------------------------------------------------------------------------

// startSubscription maintains a persistent connection to sigild, subscribing
// to the "suggestions" topic. On disconnect it reconnects with exponential
// backoff (1 s initial, doubling, capped at 30 s, reset on success).
func (a *App) startSubscription(ctx context.Context) {
	delay := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.Dial("unix", a.socketPath)
		if err != nil {
			a.setConnected(false)
			a.log.Debug("subscription connect failed, backing off", "delay", delay, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, 30*time.Second)
			continue
		}

		// Connection succeeded — reset backoff, but don't mark
		// connected until subscribe handshake completes.
		delay = time.Second
		a.mu.Lock()
		a.subConn = conn
		a.mu.Unlock()

		// Send subscribe request.
		req := socket.Request{Method: "subscribe"}
		req.Payload, _ = json.Marshal(map[string]string{"topic": "suggestions"})
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			a.log.Warn("subscription write failed", "err", err)
			conn.Close()
			a.setConnected(false)
			continue
		}

		// Read the first line (subscribe ack) to confirm daemon is live.
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			a.log.Debug("subscription: no ack from daemon")
			conn.Close()
			a.setConnected(false)
			continue
		}
		a.handlePushLine(scanner.Text())
		a.setConnected(true)

		// Read push events until disconnect.
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			default:
			}
			a.handlePushLine(scanner.Text())
		}

		conn.Close()
		a.setConnected(false)
		a.log.Info("subscription disconnected, will reconnect")
	}
}

// handlePushLine parses a single newline-delimited JSON push event and emits
// Wails events to the frontend.
func (a *App) handlePushLine(line string) {
	var push struct {
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload,omitempty"`
		OK      bool            `json:"ok,omitempty"`
	}
	if err := json.Unmarshal([]byte(line), &push); err != nil {
		return
	}

	// Ignore subscription acknowledgement (has "ok" but no "event").
	if push.Event == "" {
		return
	}

	if push.Event == "suggestions" {
		var sg map[string]any
		if err := json.Unmarshal(push.Payload, &sg); err == nil {
			wailsrt.EventsEmit(a.ctx, "suggestion:new", sg)

			title, _ := sg["title"].(string)
			body, _ := sg["text"].(string)
			if body == "" {
				body, _ = sg["body"].(string)
			}
			idFloat, _ := sg["id"].(float64)
			if title != "" {
				_ = a.notifier.Show(title, body, "", int64(idFloat))
			}
		}
	}
}

// setConnected updates the connection state and emits a Wails event.
func (a *App) setConnected(c bool) {
	a.mu.Lock()
	changed := a.connected != c
	a.connected = c
	a.mu.Unlock()

	if changed && a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "connection:changed", c)
		updateTrayStatus(c)
	}
}

// ---------------------------------------------------------------------------
// Wails runtime helpers (used by tray callbacks)
// ---------------------------------------------------------------------------

func wailsShow(ctx context.Context) {
	wailsrt.WindowShow(ctx)
}

func wailsQuit(ctx context.Context) {
	wailsrt.Quit(ctx)
}

// ---------------------------------------------------------------------------
// Wails-bound methods (exported, called from frontend JS)
// ---------------------------------------------------------------------------

// GetStatus returns the daemon status.
func (a *App) GetStatus() (map[string]any, error) {
	resp, err := a.call("status", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetSuggestions returns recent suggestions from the daemon.
func (a *App) GetSuggestions() ([]map[string]any, error) {
	resp, err := a.call("suggestions", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result []map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// AcceptSuggestion sends acceptance feedback for a suggestion.
func (a *App) AcceptSuggestion(id int) error {
	resp, err := a.call("feedback", map[string]any{
		"suggestion_id": id,
		"outcome":       "accepted",
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// DismissSuggestion sends dismissal feedback for a suggestion.
func (a *App) DismissSuggestion(id int) error {
	resp, err := a.call("feedback", map[string]any{
		"suggestion_id": id,
		"outcome":       "dismissed",
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// SetLevel changes the daemon's notification level (0-4).
func (a *App) SetLevel(n int) error {
	resp, err := a.call("set-level", map[string]any{"level": n})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// GetDaySummary returns today's work breakdown from the daemon.
func (a *App) GetDaySummary() (map[string]any, error) {
	resp, err := a.call("day-summary", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// AskContext provides optional context for AI queries.
type AskContext struct {
	Task        string   `json:"task,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	RecentFiles []string `json:"recent_files,omitempty"`
}

// Ask sends a free-text query to the daemon's inference engine.
func (a *App) Ask(query string) (map[string]any, error) {
	return a.AskWithContext(query, AskContext{})
}

// AskWithContext sends a query with optional task/branch/file context.
func (a *App) AskWithContext(query string, ctx AskContext) (map[string]any, error) {
	payload := map[string]any{"query": query}
	if ctx.Task != "" {
		payload["task"] = ctx.Task
	}
	if ctx.Branch != "" {
		payload["branch"] = ctx.Branch
	}
	if len(ctx.RecentFiles) > 0 {
		payload["recent_files"] = ctx.RecentFiles
	}
	resp, err := a.call("ai-query", payload)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetCurrentTask returns the inferred current task context.
func (a *App) GetCurrentTask() (map[string]any, error) {
	resp, err := a.call("task", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetConfig returns the daemon's current configuration with sensitive fields masked.
func (a *App) GetConfig() (map[string]any, error) {
	resp, err := a.call("config", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SetConfig sends a partial config update to the daemon.
func (a *App) SetConfig(cfg map[string]any) (map[string]any, error) {
	resp, err := a.call("set-config", cfg)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetPluginStatus returns installed plugins with their runtime status.
func (a *App) GetPluginStatus() ([]map[string]any, error) {
	resp, err := a.call("plugin-status", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result []map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetPluginRegistry returns all available plugins from the registry.
func (a *App) GetPluginRegistry() ([]map[string]any, error) {
	resp, err := a.call("plugin-registry", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result []map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// InstallPlugin installs a plugin by name.
func (a *App) InstallPlugin(name string) error {
	resp, err := a.call("plugin-install", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// EnablePlugin enables a plugin by name.
func (a *App) EnablePlugin(name string) error {
	resp, err := a.call("plugin-enable", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// DisablePlugin disables a plugin by name.
func (a *App) DisablePlugin(name string) error {
	resp, err := a.call("plugin-disable", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// StopDaemon sends a shutdown command to sigild.
func (a *App) StopDaemon() error {
	resp, err := a.call("shutdown", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// StartDaemon launches sigild as a background process.
func (a *App) StartDaemon() error {
	bin, err := exec.LookPath("sigild")
	if err != nil {
		return fmt.Errorf("sigild not found in PATH: %w", err)
	}
	cmd := exec.Command(bin, "run")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sigild: %w", err)
	}
	// Detach — don't wait for the process
	go cmd.Wait()
	return nil
}

// RestartDaemon stops and then starts the daemon.
func (a *App) RestartDaemon() error {
	// Best-effort stop — ignore errors (daemon might already be stopped)
	_ = a.StopDaemon()
	// Wait for the daemon to shut down and socket to close
	time.Sleep(2 * time.Second)
	return a.StartDaemon()
}

// IsConnected returns whether the app has an active subscription connection to
// the daemon.
func (a *App) IsConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}
