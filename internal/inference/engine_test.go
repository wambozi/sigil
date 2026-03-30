package inference

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stoppableBackend satisfies Backend and Stoppable. Records whether Stop was
// called so tests can assert on it.
type stoppableBackend struct {
	stopped bool
}

func (s *stoppableBackend) Complete(_ context.Context, _, _ string) (*CompletionResult, error) {
	return &CompletionResult{Content: "ok", Routing: "local"}, nil
}

func (s *stoppableBackend) Ping(_ context.Context) error { return nil }

func (s *stoppableBackend) Stop() error {
	s.stopped = true
	return nil
}

// nonToolBackend satisfies Backend but deliberately does not satisfy ToolBackend.
type nonToolBackend struct{}

func (n *nonToolBackend) Complete(_ context.Context, _, _ string) (*CompletionResult, error) {
	return &CompletionResult{Content: "ok", Routing: "local"}, nil
}

func (n *nonToolBackend) Ping(_ context.Context) error { return nil }

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_defaultMode(t *testing.T) {
	engine, err := New(Config{}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, RouteLocalFirst, engine.mode)
}

func TestNew_localBackendInitFails_warnAndContinue(t *testing.T) {
	// Provide an invalid ServerBin path so startServer is attempted and fails.
	// The engine should still be constructed (non-fatal failure per the contract).
	engine, err := New(Config{
		Mode: RouteLocal,
		Local: LocalConfig{
			Enabled:   true,
			ServerURL: "http://127.0.0.1:19999", // nothing listening here
			ServerBin: "/nonexistent/llama-server",
			ModelPath: "/nonexistent/model.gguf",
		},
	}, testLogger())
	require.NoError(t, err, "New should not fail even if backend init fails")
	assert.Nil(t, engine.local, "local backend should be nil after init failure")
}

func TestNew_cloudBackendInitFails_warnAndContinue(t *testing.T) {
	// NewCloud itself never fails (it only sets struct fields), so we can't
	// easily trigger the cloud-warn path via the public API without a code
	// change. The local-warn path is covered by TestNew_localBackendInitFails.
	// This test documents the invariant: engine is always non-nil from New.
	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)
	require.NotNil(t, engine)
}

func TestNew_localWarn_noModelConfigured(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	engine, err := New(Config{
		Mode: RouteLocal,
		Local: LocalConfig{
			Enabled:   true,
			ServerURL: "http://127.0.0.1:19998",
			ServerBin: "/bin/true",
			ModelPath: "",
		},
	}, testLogger())
	require.NoError(t, err, "New must not fail even when local backend init fails")
	assert.Nil(t, engine.local, "local backend should be nil when startServer fails before exec")
}

// ---------------------------------------------------------------------------
// Complete — routing modes
// ---------------------------------------------------------------------------

func TestComplete_localMode(t *testing.T) {
	ts := mockOpenAIServer("local response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "system", "hello")
	require.NoError(t, err)
	assert.Equal(t, "local response", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestComplete_cloudMode(t *testing.T) {
	ts := mockOpenAIServer("cloud response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "system", "hello")
	require.NoError(t, err)
	assert.Equal(t, "cloud response", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestComplete_localFirstFallback(t *testing.T) {
	// Local server that always fails.
	failServer := failingServer()
	defer failServer.Close()

	cloudServer := mockOpenAIServer("cloud fallback")
	defer cloudServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "", "hello")
	require.NoError(t, err, "expected fallback to cloud")
	assert.Equal(t, "cloud fallback", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestComplete_remoteFirstFallback(t *testing.T) {
	failServer := failingServer()
	defer failServer.Close()

	localServer := mockOpenAIServer("local fallback")
	defer localServer.Close()

	engine, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: localServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: failServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "", "hello")
	require.NoError(t, err, "expected fallback to local")
	assert.Equal(t, "local fallback", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestComplete_remoteFirstCloudNil(t *testing.T) {
	// RouteRemoteFirst with no cloud backend configured: completeCloud returns error.
	engine, err := New(Config{Mode: RouteRemoteFirst}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestComplete_noBackendConfigured(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestEngine_Complete_localFirstSuccess(t *testing.T) {
	ts := mockOpenAIServer("local first success")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "", "hi")
	require.NoError(t, err)
	assert.Equal(t, "local first success", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestEngine_Complete_remoteFirstSuccess(t *testing.T) {
	// Both backends healthy; cloud is preferred and succeeds — no fallback.
	cloudServer := mockOpenAIServer("cloud wins")
	defer cloudServer.Close()
	localServer := mockOpenAIServer("local would win if fallback")
	defer localServer.Close()

	engine, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: localServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "", "hello")
	require.NoError(t, err)
	assert.Equal(t, "cloud wins", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestComplete_anthropicBackend(t *testing.T) {
	ts := mockAnthropicServer("anthropic response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.Complete(context.Background(), "you are helpful", "hello")
	require.NoError(t, err)
	assert.Equal(t, "anthropic response", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestComplete_anthropicNoContent(t *testing.T) {
	ts := emptyAnthropicServer()
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestComplete_anthropicHTTPError(t *testing.T) {
	ts := rateLimitedServer()
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestComplete_emptyChoices(t *testing.T) {
	ts := emptyChoicesServer()
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestComplete_httpError(t *testing.T) {
	ts := failingServer()
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	_, err = engine.Complete(context.Background(), "", "test")
	require.Error(t, err)
}

func TestComplete_noSystemPrompt(t *testing.T) {
	var capturedMessages []map[string]any

	ts := capturingServer(&capturedMessages, "ok")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	engine.Complete(context.Background(), "", "user only") //nolint:errcheck

	assert.Len(t, capturedMessages, 1, "expected 1 message (no system) when system prompt is empty")
	if len(capturedMessages) > 0 {
		assert.Equal(t, "user", capturedMessages[0]["role"])
	}
}

// ---------------------------------------------------------------------------
// CompleteWithTools — routing modes
// ---------------------------------------------------------------------------

func TestEngine_CompleteWithTools_localMode(t *testing.T) {
	ts := mockToolServer("tool result", nil)
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "tool result", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestEngine_CompleteWithTools_remoteMode(t *testing.T) {
	ts := mockToolServer("cloud tool result", nil)
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "cloud tool result", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestEngine_CompleteWithTools_localFirstFallback(t *testing.T) {
	failServer := failingServer()
	defer failServer.Close()

	cloudServer := mockToolServer("cloud tool fallback", nil)
	defer cloudServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "cloud tool fallback", result.Content)
	assert.Equal(t, "cloud", result.Routing)
}

func TestEngine_CompleteWithTools_remoteFirstFallback(t *testing.T) {
	failServer := failingServer()
	defer failServer.Close()

	localServer := mockToolServer("local tool fallback", nil)
	defer localServer.Close()

	engine, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: localServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: failServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "local tool fallback", result.Content)
	assert.Equal(t, "local", result.Routing)
}

func TestEngine_CompleteWithTools_localFirstSuccess(t *testing.T) {
	ts := mockToolServer("localfirst tool success", nil)
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	result, err := engine.CompleteWithTools(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "localfirst tool success", result.Content)
}

func TestEngine_CompleteWithTools_noLocalBackend(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	require.NoError(t, err)

	_, err = engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local backend not configured")
}

func TestEngine_CompleteWithTools_noCloudBackend(t *testing.T) {
	engine, err := New(Config{Mode: RouteRemote}, testLogger())
	require.NoError(t, err)

	_, err = engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud backend not configured")
}

func TestEngine_CompleteWithTools_remoteFirstNoBackend(t *testing.T) {
	engine, err := New(Config{Mode: RouteRemoteFirst}, testLogger())
	require.NoError(t, err)

	_, err = engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
}

func TestEngine_CompleteWithTools_localFirstOnlyFailingLocal(t *testing.T) {
	// LocalFirst with a failing local and no cloud: returns local error.
	failServer := failingServer()
	defer failServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
	}, testLogger())
	require.NoError(t, err)

	_, err = engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
}

func TestEngine_completeWithToolsLocal_nonToolBackend(t *testing.T) {
	engine := &Engine{
		local: &nonToolBackend{},
		mode:  RouteLocal,
		log:   testLogger(),
	}

	_, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support tool calling")
}

func TestEngine_completeWithToolsCloud_nonToolBackend(t *testing.T) {
	engine := &Engine{
		cloud: &nonToolBackend{},
		mode:  RouteRemote,
		log:   testLogger(),
	}

	_, err := engine.CompleteWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support tool calling")
}

// ---------------------------------------------------------------------------
// Ping
// ---------------------------------------------------------------------------

func TestPing_local(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_cloud(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_noBackend(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	require.NoError(t, err)

	require.Error(t, engine.Ping(context.Background()))
}

func TestPing_anthropicBackend(t *testing.T) {
	ts := mockAnthropicServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_localFirstBothPresent(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_localFirstLocalFailsCloudSucceeds(t *testing.T) {
	failServer := failingServer()
	defer failServer.Close()

	cloudServer := mockOpenAIServer("")
	defer cloudServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_localFirstNoBackends(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocalFirst}, testLogger())
	require.NoError(t, err)

	require.Error(t, engine.Ping(context.Background()))
}

func TestPing_cloudNonOKStatus(t *testing.T) {
	ts := unavailableServer()
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	require.NoError(t, err)

	require.Error(t, engine.Ping(context.Background()))
}

func TestPing_remoteFirstOnlyLocalAvailable(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Ping(context.Background()))
}

func TestPing_remoteFirstNoBackends(t *testing.T) {
	engine, err := New(Config{Mode: RouteRemoteFirst}, testLogger())
	require.NoError(t, err)

	err = engine.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no backend reachable")
}

func TestPing_cloudBackendNilRouteRemote(t *testing.T) {
	engine, err := New(Config{Mode: RouteRemote}, testLogger())
	require.NoError(t, err)

	err = engine.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud backend not configured")
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestEngineClose_noBackends(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Close())
}

func TestEngineClose_stoppableLocalBackend(t *testing.T) {
	sb := &stoppableBackend{}
	engine := &Engine{
		local: sb,
		mode:  RouteLocal,
		log:   testLogger(),
	}

	require.NoError(t, engine.Close())
	assert.True(t, sb.stopped, "expected Stop to be called on the local backend")
}

// mockLocalInfoBackend satisfies Backend and localInfoProvider for testing Engine accessors.
type mockLocalInfoBackend struct {
	stoppableBackend
	pid     int
	managed bool
	ok      bool
	model   string
	ctxSize int
}

func (m *mockLocalInfoBackend) ProcessInfo() (int, bool, bool) {
	return m.pid, m.managed, m.ok
}

func (m *mockLocalInfoBackend) ModelName() string { return m.model }

func (m *mockLocalInfoBackend) CtxSize() int { return m.ctxSize }

func TestEngineLocalProcessInfo_withLocalBackend(t *testing.T) {
	lb := &mockLocalInfoBackend{pid: 1234, managed: true, ok: true}
	engine := &Engine{local: lb, mode: RouteLocal, log: testLogger()}

	pid, managed, ok := engine.LocalProcessInfo()
	assert.Equal(t, 1234, pid)
	assert.True(t, managed)
	assert.True(t, ok)
}

func TestEngineLocalProcessInfo_noProcess(t *testing.T) {
	lb := &mockLocalInfoBackend{pid: 0, managed: false, ok: false}
	engine := &Engine{local: lb, mode: RouteLocal, log: testLogger()}

	pid, managed, ok := engine.LocalProcessInfo()
	assert.Equal(t, 0, pid)
	assert.False(t, managed)
	assert.False(t, ok)
}

func TestEngineLocalProcessInfo_nilLocalBackend(t *testing.T) {
	engine := &Engine{local: nil, mode: RouteRemote, log: testLogger()}

	pid, managed, ok := engine.LocalProcessInfo()
	assert.Equal(t, 0, pid)
	assert.False(t, managed)
	assert.False(t, ok)
}

func TestEngineLocalProcessInfo_nonInfoBackend(t *testing.T) {
	// A Backend that doesn't implement localInfoProvider.
	engine := &Engine{local: &stoppableBackend{}, mode: RouteLocal, log: testLogger()}

	pid, managed, ok := engine.LocalProcessInfo()
	assert.Equal(t, 0, pid)
	assert.False(t, managed)
	assert.False(t, ok)
}

func TestEngineLocalModelName(t *testing.T) {
	lb := &mockLocalInfoBackend{model: "qwen2.5-1.5b-q4_k_m"}
	engine := &Engine{local: lb, mode: RouteLocal, log: testLogger()}
	assert.Equal(t, "qwen2.5-1.5b-q4_k_m", engine.LocalModelName())
}

func TestEngineLocalModelName_nilBackend(t *testing.T) {
	engine := &Engine{local: nil, mode: RouteRemote, log: testLogger()}
	assert.Equal(t, "", engine.LocalModelName())
}

func TestEngineLocalCtxSize(t *testing.T) {
	lb := &mockLocalInfoBackend{ctxSize: 8192}
	engine := &Engine{local: lb, mode: RouteLocal, log: testLogger()}
	assert.Equal(t, 8192, engine.LocalCtxSize())
}

func TestEngineLocalCtxSize_nilBackend(t *testing.T) {
	engine := &Engine{local: nil, mode: RouteRemote, log: testLogger()}
	assert.Equal(t, 0, engine.LocalCtxSize())
}

func TestEngineClose_nonStoppableLocalBackend(t *testing.T) {
	// A local backend that only satisfies Backend (not Stoppable) must not
	// cause Close to error — the type assertion is a no-op.
	ts := mockOpenAIServer("ok")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, engine.Close())
}
