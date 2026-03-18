package ml

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
	"sync"
	"syscall"
	"time"
)

const (
	mlMaxRestarts     = 3
	mlHealthInterval  = 30 * time.Second
	mlStartupTimeout  = 30 * time.Second
	mlShutdownTimeout = 5 * time.Second
)

// LocalBackend talks to a local sigil-ml FastAPI server.
// If ServerBin is configured and no server is already running, it starts
// sigil-ml as a managed subprocess.
type LocalBackend struct {
	baseURL   string
	client    *http.Client
	log       *slog.Logger
	proc      *os.Process
	managed   bool
	mu        sync.Mutex
	restarts  int
	cfg       LocalConfig
	stopOnce  sync.Once
	healthCtx context.Context
	healthCfn context.CancelFunc
}

// NewLocal creates a LocalBackend. If ServerBin is configured and no server
// is already responding, it starts sigil-ml as a managed subprocess.
func NewLocal(cfg LocalConfig, log *slog.Logger) (*LocalBackend, error) {
	url := cfg.ServerURL
	if url == "" {
		url = "http://127.0.0.1:7774"
	}

	healthCtx, healthCfn := context.WithCancel(context.Background())

	l := &LocalBackend{
		baseURL:   url,
		client:    &http.Client{Timeout: 30 * time.Second},
		log:       log,
		cfg:       cfg,
		healthCtx: healthCtx,
		healthCfn: healthCfn,
	}

	// Check if a server is already running.
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := l.Ping(pingCtx)
	cancel()

	if err == nil {
		log.Info("ml/local: server already running", "url", url)
		return l, nil
	}

	// Start the server if we have a binary configured.
	if cfg.ServerBin != "" {
		if err := l.startServer(); err != nil {
			return nil, fmt.Errorf("ml/local: start server: %w", err)
		}
		go l.healthMonitor()
	}

	return l, nil
}

// Predict calls a prediction endpoint on the local server.
func (l *LocalBackend) Predict(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error) {
	body, err := json.Marshal(map[string]any{"features": features})
	if err != nil {
		return nil, err
	}

	url := l.baseURL + "/predict/" + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := l.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("ml/local: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ml/local: %s: HTTP %d: %s", endpoint, resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ml/local: %s: decode: %w", endpoint, err)
	}

	return &Prediction{
		Endpoint:  endpoint,
		Result:    result,
		Routing:   "local",
		LatencyMS: latency,
	}, nil
}

// Train triggers model retraining on the local server.
func (l *LocalBackend) Train(ctx context.Context, dbPath string) (*TrainResult, error) {
	body, _ := json.Marshal(map[string]string{"db_path": dbPath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/train", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ml/local: train: %w", err)
	}
	defer resp.Body.Close()

	var result TrainResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Training is async — the server returns a status message, not full result.
		return &TrainResult{}, nil
	}
	return &result, nil
}

// Ping checks if the server is healthy.
func (l *LocalBackend) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml/local: health check returned %d", resp.StatusCode)
	}
	return nil
}

// Stop terminates the managed subprocess.
func (l *LocalBackend) Stop() error {
	l.healthCfn()
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.managed || l.proc == nil {
		return nil
	}

	var stopErr error
	l.stopOnce.Do(func() {
		l.log.Info("ml/local: stopping managed server")
		stopErr = l.killProcess()
	})
	return stopErr
}

func (l *LocalBackend) startServer() error {
	bin := l.cfg.ServerBin
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("binary %q not found in PATH: %w", bin, err)
	}

	// Parse port from URL for the --port flag.
	port := "7774"
	if len(l.baseURL) > 0 {
		// Simple extraction: last segment after ':'
		for i := len(l.baseURL) - 1; i >= 0; i-- {
			if l.baseURL[i] == ':' {
				port = l.baseURL[i+1:]
				break
			}
		}
	}

	cmd := exec.Command(bin, "serve", "--port", port)
	cmd.Stdout = os.Stderr // route to daemon stderr for visibility
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}

	l.mu.Lock()
	l.proc = cmd.Process
	l.managed = true
	l.mu.Unlock()

	// Reap the process in the background.
	go func() { _ = cmd.Wait() }()

	l.log.Info("ml/local: starting server", "pid", cmd.Process.Pid, "port", port)

	if err := l.waitForHealth(); err != nil {
		l.killProcess()
		return fmt.Errorf("server did not become healthy: %w", err)
	}

	l.log.Info("ml/local: server ready")
	return nil
}

func (l *LocalBackend) waitForHealth() error {
	deadline := time.Now().Add(mlStartupTimeout)
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
	return fmt.Errorf("timeout after %s", mlStartupTimeout)
}

func (l *LocalBackend) healthMonitor() {
	ticker := time.NewTicker(mlHealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.healthCtx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(l.healthCtx, 5*time.Second)
			err := l.Ping(ctx)
			cancel()
			if err == nil {
				continue
			}

			l.mu.Lock()
			if l.restarts >= mlMaxRestarts {
				l.mu.Unlock()
				l.log.Error("ml/local: server crashed too many times, giving up")
				continue
			}
			l.restarts++
			restartNum := l.restarts
			l.mu.Unlock()

			l.log.Warn("ml/local: server not responding, restarting",
				"restart", restartNum, "max", mlMaxRestarts)
			l.killProcess()
			backoff := time.Duration(1<<uint(restartNum-1)) * time.Second
			time.Sleep(backoff)
			if err := l.startServer(); err != nil {
				l.log.Error("ml/local: restart failed", "err", err)
			}
		}
	}
}

func (l *LocalBackend) killProcess() error {
	l.mu.Lock()
	proc := l.proc
	l.proc = nil
	l.mu.Unlock()

	if proc == nil {
		return nil
	}

	_ = proc.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		l.log.Debug("ml/local: server exited gracefully")
	case <-time.After(mlShutdownTimeout):
		l.log.Warn("ml/local: server did not exit, sending SIGKILL")
		_ = proc.Signal(syscall.SIGKILL)
		<-done
	}
	return nil
}
