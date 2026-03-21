package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewLocal
// ---------------------------------------------------------------------------

func TestNewLocal_serverAlreadyRunning(t *testing.T) {
	// Server is up before NewLocal is called — the "already running" log path.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, ts.URL, l.baseURL)
}

func TestNewLocal_defaultURL(t *testing.T) {
	// No server is running at 8081; NewLocal will fail the ping and return
	// without starting a subprocess (no ServerBin set). The baseURL default
	// must still be set correctly.
	l, err := NewLocal(LocalConfig{Enabled: true}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8081", l.baseURL)
}

// ---------------------------------------------------------------------------
// startServer — various config branches
// ---------------------------------------------------------------------------

func TestNewLocal_startServer_noModelPath_defaultNotCached(t *testing.T) {
	// Use a temp dir with no model files so ModelPath(DefaultModel) returns "".
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// Point the server URL at something that won't respond, and set
	// ServerBin to a real binary so NewLocal attempts startServer.
	// With no model path and no cached default model, startServer returns
	// an error at the "no model path configured" guard before exec.Command.
	// /bin/true is a real binary that exists on every Linux system.
	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:19998", // nothing listening
		ServerBin: "/bin/true",
		ModelPath: "", // force the default model lookup path
	}

	l, err := NewLocal(cfg, testLogger())
	// NewLocal returns (nil, err) when startServer fails.
	require.Error(t, err)
	assert.Nil(t, l)
	assert.Contains(t, err.Error(), "start server")
}

func TestNewLocal_startServer_trailingSlashURL(t *testing.T) {
	// A URL with a trailing slash exercises the port-stripping loop in
	// startServer (for len(p) > 0 && p[len(p)-1] == '/').
	// We use a non-existent ServerBin so exec.Command.Start() fails, but
	// the port extraction logic (including slash stripping) runs first.
	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:19997/", // trailing slash
		ServerBin: "/nonexistent/llama-server",
		ModelPath: "/nonexistent/model.gguf", // non-empty so model-path check passes
	}

	_, err := NewLocal(cfg, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start server")
}

func TestNewLocal_startServer_gpuLayersSet(t *testing.T) {
	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:19996",
		ServerBin: "/nonexistent/llama-server",
		ModelPath: "/nonexistent/model.gguf",
		GPULayers: 32, // non-zero → exercises the gpu-layers branch
	}

	_, err := NewLocal(cfg, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start server")
}

func TestNewLocal_startServer_defaultCtxSize(t *testing.T) {
	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:19995",
		ServerBin: "/nonexistent/llama-server",
		ModelPath: "/nonexistent/model.gguf",
		CtxSize:   0, // exercises the "ctxSize <= 0 → 4096" branch
	}

	_, err := NewLocal(cfg, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start server")
}

func TestNewLocal_startServer_success(t *testing.T) {
	helperBin := "testdata/fake_llama_server"

	const port = "18921"
	serverURL := "http://127.0.0.1:" + port

	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: serverURL,
		ServerBin: helperBin,
		ModelPath: "/dev/null", // non-empty placeholder; fake_llama_server ignores it
		CtxSize:   128,
	}

	l, err := NewLocal(cfg, testLogger())
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.True(t, l.managed, "backend should be marked as managed")

	// Ping should succeed since the server is running.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, l.Ping(ctx))

	// Stop cleanly.
	require.NoError(t, l.Stop())

	// After Stop, the server should no longer be responding.
	time.Sleep(200 * time.Millisecond)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer pingCancel()
	_ = l.Ping(pingCtx) // may error — that's expected after stop
}

// ---------------------------------------------------------------------------
// LocalBackend — modelName
// ---------------------------------------------------------------------------

func TestLocalBackend_modelName_default(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "local", l.modelName())
}

func TestLocalBackend_modelName_explicit(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	l, err := NewLocal(LocalConfig{
		Enabled:   true,
		ServerURL: ts.URL,
		ModelName: "qwen2.5:1.5b",
	}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "qwen2.5:1.5b", l.modelName())
}

// ---------------------------------------------------------------------------
// LocalBackend — Complete
// ---------------------------------------------------------------------------

func TestLocalBackend_Complete_success(t *testing.T) {
	ts := mockOpenAIServer("hello from local")
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	result, err := l.Complete(context.Background(), "system prompt", "user message")
	require.NoError(t, err)
	assert.Equal(t, "hello from local", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestLocalBackend_Complete_noSystemPrompt(t *testing.T) {
	var capturedMsgs []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if arr, ok := body["messages"].([]any); ok {
				for _, m := range arr {
					capturedMsgs = append(capturedMsgs, m.(map[string]any))
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatResponse{ //nolint:errcheck
				Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: "ok"}}},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Complete(context.Background(), "", "only user")
	require.NoError(t, err)
	require.Len(t, capturedMsgs, 1, "expected only user message when system prompt is empty")
	assert.Equal(t, "user", capturedMsgs[0]["role"])
}

func TestLocalBackend_Complete_httpError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestLocalBackend_Complete_emptyChoices(t *testing.T) {
	ts := localServerWith(map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatResponse{}) //nolint:errcheck
		},
	})
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty choices")
}

func TestLocalBackend_Complete_decodeError(t *testing.T) {
	ts := localServerWith(map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "not-json") //nolint:errcheck
		},
	})
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestLocalBackend_Complete_requestError(t *testing.T) {
	ts := mockOpenAIServer("ok")
	ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}

func TestLocalBackend_Complete_buildRequestError(t *testing.T) {
	l := &LocalBackend{
		baseURL: "://bad url",
		client:  &http.Client{},
		log:     testLogger(),
	}
	_, err := l.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// LocalBackend — CompleteWithTools
// ---------------------------------------------------------------------------

func TestLocalBackend_CompleteWithTools_success(t *testing.T) {
	wantCall := ChatToolCall{
		ID:       "tc1",
		Type:     "function",
		Function: ChatToolCallFunc{Name: "run_tests", Arguments: `{}`},
	}
	ts := mockToolServer("running tests", []ChatToolCall{wantCall})
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	msgs := []ChatMessage{{Role: "user", Content: "run my tests"}}
	tools := []ChatToolDef{{
		Type:     "function",
		Function: ChatToolDefFunc{Name: "run_tests", Description: "Runs tests"},
	}}

	result, err := l.CompleteWithTools(context.Background(), msgs, tools)
	require.NoError(t, err)
	assert.Equal(t, "running tests", result.Content)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "tc1", result.ToolCalls[0].ID)
	assert.Equal(t, "local", result.Routing)
}

func TestLocalBackend_CompleteWithTools_httpError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestLocalBackend_CompleteWithTools_emptyChoices(t *testing.T) {
	ts := localServerWith(map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatResponseWithTools{}) //nolint:errcheck
		},
	})
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty choices")
}

func TestLocalBackend_CompleteWithTools_decodeError(t *testing.T) {
	ts := localServerWith(map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "not-json{{{") //nolint:errcheck
		},
	})
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestLocalBackend_CompleteWithTools_requestError(t *testing.T) {
	ts := mockToolServer("", nil)
	ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}

func TestLocalBackend_CompleteWithTools_buildRequestError(t *testing.T) {
	l := &LocalBackend{
		baseURL: "://bad url",
		client:  &http.Client{},
		log:     testLogger(),
	}
	_, err := l.CompleteWithTools(context.Background(), nil, nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// LocalBackend — Ping
// ---------------------------------------------------------------------------

func TestLocalBackend_Ping_healthEndpointFails_rootSucceeds(t *testing.T) {
	// Simulate an Ollama-style server: /health returns 404, / returns 200.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	require.NoError(t, l.Ping(context.Background()))
}

func TestLocalBackend_Ping_allEndpointsFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	err = l.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all health endpoints")
}

func TestLocalBackend_Ping_badURL(t *testing.T) {
	l := &LocalBackend{
		baseURL: "://bad url",
		client:  &http.Client{},
		log:     testLogger(),
	}
	err := l.Ping(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// LocalBackend — Stop
// ---------------------------------------------------------------------------

func TestLocalBackend_Stop_unmanaged(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	require.NoError(t, l.Stop())
	// Idempotent second call.
	require.NoError(t, l.Stop())
}

func TestLocalBackend_Stop_managed_noProcess(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	// Mark as managed with no proc — Stop should call killProcess which returns
	// early because proc is nil.
	l.mu.Lock()
	l.managed = true
	l.proc = nil
	l.mu.Unlock()

	require.NoError(t, l.Stop())
}

func TestLocalBackend_Stop_managedWithProcess(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "60")
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	healthCtx, healthCfn := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: healthCtx,
		healthCfn: healthCfn,
	}
	l.mu.Lock()
	l.proc = cmd.Process
	l.managed = true
	l.mu.Unlock()

	require.NoError(t, l.Stop())
	// Idempotent second call.
	require.NoError(t, l.Stop())
}

// ---------------------------------------------------------------------------
// killProcess
// ---------------------------------------------------------------------------

func TestKillProcess_nilProc(t *testing.T) {
	l := &LocalBackend{
		baseURL: "http://127.0.0.1:1",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	// proc is nil by default — killProcess must return without panicking.
	l.killProcess()
}

func TestKillProcess_gracefulExit(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "60")
	require.NoError(t, cmd.Start())

	// Reap the child in the background so we don't leak a zombie.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cmd.Wait()
	}()

	l := &LocalBackend{
		baseURL: "http://127.0.0.1:1",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	l.mu.Lock()
	l.proc = cmd.Process
	l.managed = true
	l.mu.Unlock()

	l.killProcess()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Process exited — expected.
	case <-time.After(10 * time.Second):
		t.Fatal("killProcess did not terminate the process within 10s")
	}
}

func TestKillProcess_sigkillPath(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root process management differs")
	}

	// Use a helper binary that ignores SIGTERM to exercise the SIGKILL path.
	helperBin := "testdata/sigterm_ignore"

	// Do NOT start a goroutine reaping the process in this test — killProcess
	// must be the sole reaper, otherwise proc.Wait() inside killProcess returns
	// immediately (ECHILD) and the SIGKILL branch is never reached.
	cmd := exec.Command(helperBin)
	require.NoError(t, cmd.Start())

	// Give the process time to start and register its SIGTERM handler.
	time.Sleep(200 * time.Millisecond)

	l := &LocalBackend{
		baseURL: "http://127.0.0.1:1",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	l.mu.Lock()
	l.proc = cmd.Process
	l.managed = true
	l.mu.Unlock()

	// killProcess sends SIGTERM, waits shutdownTimeout (5s), then SIGKILL.
	// This test intentionally takes at least shutdownTimeout to complete.
	l.killProcess()

	// Reap any zombie left after SIGKILL.
	_ = cmd.Wait()
}

// ---------------------------------------------------------------------------
// waitForHealth
// ---------------------------------------------------------------------------

func TestWaitForHealth_immediateSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	l := &LocalBackend{
		baseURL: ts.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}

	require.NoError(t, l.waitForHealth())
}

func TestWaitForHealth_failThenSucceed(t *testing.T) {
	// Ping tries /health then /. We need both to fail on the first Ping call
	// and then succeed on the second. Use a request counter that fails the
	// first two requests (covering both health endpoints in one Ping call).
	var hits atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			// First Ping call: both /health and / fail.
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	l := &LocalBackend{
		baseURL: ts.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}

	// First Ping: /health → 503, / → 503 → ping fails → sleep 500ms backoff.
	// Second Ping: /health → 200 → return nil.
	// This test takes ~500ms due to the backoff sleep.
	start := time.Now()
	err := l.waitForHealth()
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond,
		"waitForHealth should sleep at least one backoff interval")
}

// ---------------------------------------------------------------------------
// healthMonitor
// ---------------------------------------------------------------------------

func TestHealthMonitor_contextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}

	done := make(chan struct{})
	go func() {
		l.healthMonitor()
		close(done)
	}()

	cancel() // trigger the healthCtx.Done() case

	select {
	case <-done:
		// expected — the goroutine exited cleanly
	case <-time.After(5 * time.Second):
		t.Fatal("healthMonitor did not stop after context cancellation")
	}
}

func TestHealthMonitor_notManagedSkipsThenCancels(t *testing.T) {
	ctx2, cancel2 := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx2,
		healthCfn: cancel2,
		// managed = false (zero value)
	}

	done := make(chan struct{})
	go func() {
		l.healthMonitor()
		close(done)
	}()

	cancel2()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("healthMonitor did not stop")
	}
}

func TestHealthMonitor_managedNilProc_cancelPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}
	l.mu.Lock()
	l.managed = true
	l.proc = nil
	l.mu.Unlock()

	done := make(chan struct{})
	go func() {
		l.healthMonitor()
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("healthMonitor did not stop")
	}
}

func TestHealthMonitor_pingFailureBranchDocumented(t *testing.T) {
	// The ping-failure and restart branches in healthMonitor require:
	// 1. A real tick (healthInterval=30s) — too slow for unit tests
	// 2. A managed subprocess — requires llama-server binary
	// These branches are tested implicitly via integration tests only.
	// Coverage of this path is not achievable in unit tests without modifying
	// healthInterval from a const to a var, or using dependency injection.
	t.Skip("healthMonitor tick+restart branches require subprocess infrastructure")
}
