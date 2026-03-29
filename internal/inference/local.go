package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// LocalBackend talks to a local OpenAI-compatible server (e.g. llama-server).
// If ServerBin is configured and no server is already running, it starts
// llama-server as a managed subprocess.
type LocalBackend struct {
	baseURL   string
	client    *http.Client
	log       *slog.Logger
	proc      *os.Process // managed subprocess (nil if external)
	managed   bool        // true if we started the server
	mu        sync.Mutex
	restarts  int // restart count for backoff
	cfg       LocalConfig
	stopOnce  sync.Once
	healthCtx context.Context
	healthCfn context.CancelFunc
}

const (
	maxRestarts     = 3
	healthInterval  = 30 * time.Second
	startupTimeout  = 60 * time.Second
	shutdownTimeout = 5 * time.Second
)

// NewLocal creates a LocalBackend. If ServerBin is configured and no server
// is already responding, it starts llama-server as a managed subprocess.
func NewLocal(cfg LocalConfig, log *slog.Logger) (*LocalBackend, error) {
	url := cfg.ServerURL
	if url == "" {
		url = "http://127.0.0.1:8081"
	}

	healthCtx, healthCfn := context.WithCancel(context.Background())

	l := &LocalBackend{
		baseURL: url,
		client: &http.Client{
			Timeout: 5 * time.Minute, // local LLM on CPU with tools can be slow
		},
		log:       log,
		cfg:       cfg,
		healthCtx: healthCtx,
		healthCfn: healthCfn,
	}

	// Check if a server is already running at the URL.
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := l.Ping(pingCtx)
	cancel()

	if err == nil {
		log.Info("inference/local: server already running", "url", url)
		return l, nil
	}

	// If ServerBin is set, start the server as a subprocess.
	if cfg.ServerBin != "" {
		if err := l.startServer(); err != nil {
			return nil, fmt.Errorf("inference/local: start server: %w", err)
		}
		// Start health monitor goroutine.
		go l.healthMonitor()
	}

	return l, nil
}

// startServer launches llama-server as a subprocess.
func (l *LocalBackend) startServer() error {
	modelPath := l.cfg.ModelPath
	if modelPath == "" {
		// Try to use the default model.
		modelPath = ModelPath(DefaultModel)
		if modelPath == "" {
			return fmt.Errorf("no model path configured and default model not cached; run 'sigilctl model pull'")
		}
	}

	// Parse port from URL.
	port := "8081"
	if l.baseURL != "" {
		// Extract port from http://host:port
		for i := len(l.baseURL) - 1; i >= 0; i-- {
			if l.baseURL[i] == ':' {
				p := l.baseURL[i+1:]
				// Strip trailing slash.
				for len(p) > 0 && p[len(p)-1] == '/' {
					p = p[:len(p)-1]
				}
				if _, err := strconv.Atoi(p); err == nil {
					port = p
				}
				break
			}
		}
	}

	ctxSize := l.cfg.CtxSize
	if ctxSize <= 0 {
		ctxSize = 4096
	}

	args := []string{
		"--model", modelPath,
		"--port", port,
		"--ctx-size", strconv.Itoa(ctxSize),
	}

	if l.cfg.GPULayers != 0 {
		args = append(args, "--n-gpu-layers", strconv.Itoa(l.cfg.GPULayers))
	}

	l.log.Info("inference/local: starting llama-server",
		"bin", l.cfg.ServerBin, "model", modelPath, "port", port)

	cmd := exec.Command(l.cfg.ServerBin, args...)
	cmd.Stdout = os.Stderr // llama-server logs go to daemon stderr
	cmd.Stderr = os.Stderr
	// Set process group so we can kill the entire group on shutdown.
	setProcGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", l.cfg.ServerBin, err)
	}

	l.mu.Lock()
	l.proc = cmd.Process
	l.managed = true
	l.mu.Unlock()

	// Wait for the server to become healthy.
	if err := l.waitForHealth(); err != nil {
		// Kill the process if it didn't come up.
		l.killProcess()
		return fmt.Errorf("server did not become healthy: %w", err)
	}

	l.log.Info("inference/local: llama-server started", "pid", cmd.Process.Pid)

	// Reap the process in the background so we don't leak zombies.
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// waitForHealth polls the health endpoint until the server responds OK.
func (l *LocalBackend) waitForHealth() error {
	deadline := time.Now().Add(startupTimeout)
	backoff := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := l.Ping(ctx)
		cancel()

		if err == nil {
			return nil
		}

		time.Sleep(backoff)
		if backoff < 5*time.Second {
			backoff = backoff * 3 / 2
		}
	}

	return fmt.Errorf("timeout after %s", startupTimeout)
}

// healthMonitor periodically pings the server and restarts if it crashes.
func (l *LocalBackend) healthMonitor() {
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.healthCtx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			managed := l.managed
			proc := l.proc
			l.mu.Unlock()

			if !managed || proc == nil {
				continue
			}

			ctx, cancel := context.WithTimeout(l.healthCtx, 5*time.Second)
			err := l.Ping(ctx)
			cancel()

			if err != nil {
				l.mu.Lock()
				restarts := l.restarts
				l.mu.Unlock()

				if restarts >= maxRestarts {
					l.log.Error("inference/local: llama-server crashed too many times, giving up",
						"restarts", restarts)
					continue
				}

				l.log.Warn("inference/local: llama-server not responding, restarting",
					"err", err, "restart", restarts+1)

				l.killProcess()

				backoff := time.Duration(1<<uint(restarts)) * time.Second
				time.Sleep(backoff)

				if err := l.startServer(); err != nil {
					l.log.Error("inference/local: restart failed", "err", err)
				}

				l.mu.Lock()
				l.restarts++
				l.mu.Unlock()
			}
		}
	}
}

// killProcess sends SIGTERM, waits, then SIGKILL if necessary.
func (l *LocalBackend) killProcess() {
	l.mu.Lock()
	proc := l.proc
	l.proc = nil
	l.mu.Unlock()

	if proc == nil {
		return
	}

	// Send SIGTERM (or Kill on Windows).
	_ = signalTerm(proc)

	// Wait up to shutdownTimeout for graceful exit.
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		l.log.Debug("inference/local: llama-server exited gracefully")
	case <-time.After(shutdownTimeout):
		l.log.Warn("inference/local: llama-server did not exit, sending SIGKILL")
		_ = signalKill(proc)
		<-done
	}
}

// modelName returns the model identifier for API requests.
func (l *LocalBackend) modelName() string {
	if l.cfg.ModelName != "" {
		return l.cfg.ModelName
	}
	return "local"
}

// Complete sends a chat completion request to the local server.
func (l *LocalBackend) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	msgs := make([]chatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: user})

	body, err := json.Marshal(chatRequest{
		Model:    l.modelName(),
		Messages: msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("inference/local: marshal: %w", err)
	}

	url := l.baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("inference/local: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference/local: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("inference/local: HTTP %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("inference/local: decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("inference/local: empty choices")
	}

	return &CompletionResult{
		Content:   cr.Choices[0].Message.Content,
		Routing:   "local",
		LatencyMS: elapsed,
	}, nil
}

// CompleteWithTools sends a tool-calling chat completion request to the local server.
func (l *LocalBackend) CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error) {
	type toolRequest struct {
		Model    string        `json:"model"`
		Messages []ChatMessage `json:"messages"`
		Tools    []ChatToolDef `json:"tools,omitempty"`
	}

	body, err := json.Marshal(toolRequest{
		Model:    l.modelName(),
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return nil, fmt.Errorf("inference/local: marshal: %w", err)
	}

	url := l.baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("inference/local: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference/local: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("inference/local: HTTP %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponseWithTools
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("inference/local: decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("inference/local: empty choices")
	}

	return &ToolCompletionResult{
		Content:   cr.Choices[0].Message.Content,
		ToolCalls: cr.Choices[0].Message.ToolCalls,
		Routing:   "local",
		LatencyMS: elapsed,
	}, nil
}

// Ping checks whether the local server is healthy.
// Tries /health (llama-server) first, falls back to / (Ollama).
func (l *LocalBackend) Ping(ctx context.Context) error {
	for _, path := range []string{"/health", "/"} {
		url := l.baseURL + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := l.client.Do(req)
		if err != nil {
			return fmt.Errorf("inference/local: ping: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	return fmt.Errorf("inference/local: ping failed on all health endpoints")
}

// Stop shuts down the local backend. If we started llama-server, kills it.
func (l *LocalBackend) Stop() error {
	var err error
	l.stopOnce.Do(func() {
		l.healthCfn() // stop health monitor
		l.mu.Lock()
		managed := l.managed
		l.mu.Unlock()
		if managed {
			l.log.Info("inference/local: stopping managed llama-server")
			l.killProcess()
		}
	})
	return err
}
