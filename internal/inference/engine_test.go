package inference

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockOpenAIServer returns a test server that speaks the OpenAI chat completions API.
func mockOpenAIServer(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatResponse{
				Choices: []chatChoice{
					{Message: chatMessage{Role: "assistant", Content: content}},
				},
			})
		case "/v1/models", "/health":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestComplete_localMode(t *testing.T) {
	ts := mockOpenAIServer("local response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Complete(context.Background(), "system", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "local response" {
		t.Errorf("Content = %q, want %q", result.Content, "local response")
	}
	if result.Routing != "local" {
		t.Errorf("Routing = %q, want %q", result.Routing, "local")
	}
}

func TestComplete_cloudMode(t *testing.T) {
	ts := mockOpenAIServer("cloud response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Complete(context.Background(), "system", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "cloud response" {
		t.Errorf("Content = %q, want %q", result.Content, "cloud response")
	}
	if result.Routing != "cloud" {
		t.Errorf("Routing = %q, want %q", result.Routing, "cloud")
	}
}

func TestComplete_localFirstFallback(t *testing.T) {
	// Local server that always fails.
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	// Cloud server that works.
	cloudServer := mockOpenAIServer("cloud fallback")
	defer cloudServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Complete(context.Background(), "", "hello")
	if err != nil {
		t.Fatalf("expected fallback to cloud, got error: %v", err)
	}
	if result.Content != "cloud fallback" {
		t.Errorf("Content = %q, want %q", result.Content, "cloud fallback")
	}
	if result.Routing != "cloud" {
		t.Errorf("Routing = %q, want %q", result.Routing, "cloud")
	}
}

func TestComplete_remoteFirstFallback(t *testing.T) {
	// Cloud server that always fails.
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	// Local server that works.
	localServer := mockOpenAIServer("local fallback")
	defer localServer.Close()

	engine, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: localServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: failServer.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Complete(context.Background(), "", "hello")
	if err != nil {
		t.Fatalf("expected fallback to local, got error: %v", err)
	}
	if result.Content != "local fallback" {
		t.Errorf("Content = %q, want %q", result.Content, "local fallback")
	}
	if result.Routing != "local" {
		t.Errorf("Routing = %q, want %q", result.Routing, "local")
	}
}

func TestPing_local(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_cloud(t *testing.T) {
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_noBackend(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err == nil {
		t.Fatal("expected error when no backend configured")
	}
}

func TestComplete_emptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}

func TestComplete_httpError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestComplete_noSystemPrompt(t *testing.T) {
	var capturedMessages []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				capturedMessages = append(capturedMessages, m.(map[string]any))
			}
		}
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}},
			},
		})
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Empty system prompt — should send only the user message.
	engine.Complete(context.Background(), "", "user only")

	if len(capturedMessages) != 1 {
		t.Errorf("expected 1 message (no system), got %d", len(capturedMessages))
	}
	if len(capturedMessages) > 0 && capturedMessages[0]["role"] != "user" {
		t.Errorf("expected user role, got %q", capturedMessages[0]["role"])
	}
}

func TestComplete_noBackendConfigured(t *testing.T) {
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error when no backend configured")
	}
}

// mockAnthropicServer returns a test server that speaks the Anthropic Messages API.
func mockAnthropicServer(text string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(anthropicResponse{
				Content: []anthropicContent{
					{Type: "text", Text: text},
				},
			})
		default:
			// Respond 200 to any other request (used by Ping for anthropic format).
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func TestComplete_anthropicBackend(t *testing.T) {
	ts := mockAnthropicServer("anthropic response")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Complete(context.Background(), "you are helpful", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "anthropic response" {
		t.Errorf("Content = %q, want %q", result.Content, "anthropic response")
	}
	if result.Routing != "cloud" {
		t.Errorf("Routing = %q, want %q", result.Routing, "cloud")
	}
}

func TestPing_anthropicBackend(t *testing.T) {
	ts := mockAnthropicServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestComplete_anthropicNoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{}})
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for empty Anthropic content array, got nil")
	}
}

// stoppableBackend is a test double that satisfies both Backend and Stoppable.
// It records whether Stop was called so tests can assert on it.
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

func TestEngineClose_noBackends(t *testing.T) {
	// An engine with no backends should close without error.
	engine, err := New(Config{Mode: RouteLocal}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineClose_stoppableLocalBackend(t *testing.T) {
	sb := &stoppableBackend{}
	engine := &Engine{
		local: sb,
		mode:  RouteLocal,
		log:   testLogger(),
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sb.stopped {
		t.Error("expected Stop to be called on the local backend")
	}
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
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestModelsDir(t *testing.T) {
	dir := ModelsDir()
	if dir == "" {
		t.Fatal("ModelsDir returned empty string")
	}
	// The path must contain the well-known subdirectory components.
	for _, want := range []string{"sigild", "models"} {
		if !containsPathComponent(dir, want) {
			t.Errorf("ModelsDir = %q, expected it to contain %q", dir, want)
		}
	}
}

func TestModelsDir_respectsXDGDataHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dir := ModelsDir()
	wantSuffix := "sigild/models"
	if len(dir) < len(wantSuffix) || dir[len(dir)-len(wantSuffix):] != wantSuffix {
		t.Errorf("ModelsDir = %q, want suffix %q", dir, wantSuffix)
	}
}

func TestListCachedModels_emptyDir(t *testing.T) {
	// Point XDG_DATA_HOME at a temp dir so no model files exist on disk.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	models := ListCachedModels()
	if len(models) != 0 {
		t.Errorf("ListCachedModels = %v, want empty slice", models)
	}
}

func TestModelPath_unknownModel(t *testing.T) {
	path := ModelPath("nonexistent-model-xyz")
	if path != "" {
		t.Errorf("ModelPath = %q, want empty string for unknown model", path)
	}
}

func TestModelPath_knownModelNotCached(t *testing.T) {
	// Use a temp dir that contains no model files.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// Pick any key from KnownModels — the file won't exist on disk.
	var name string
	for k := range KnownModels {
		name = k
		break
	}

	path := ModelPath(name)
	if path != "" {
		t.Errorf("ModelPath(%q) = %q, want empty string when model is not cached", name, path)
	}
}

func TestModelPath_cachedModel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	// Pick the first known model and plant its file on disk.
	var name string
	var spec ModelSpec
	for k, v := range KnownModels {
		name, spec = k, v
		break
	}

	// Create the models directory and a zero-byte stand-in for the model file.
	modelsDir := ModelsDir()
	if err := os.MkdirAll(modelsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(modelsDir, spec.Filename))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	path := ModelPath(name)
	if path == "" {
		t.Errorf("ModelPath(%q) = empty, want non-empty path to cached file", name)
	}
}

func TestListCachedModels_withCachedFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	// Plant a file for the first known model.
	var name string
	var spec ModelSpec
	for k, v := range KnownModels {
		name, spec = k, v
		break
	}

	modelsDir := ModelsDir()
	if err := os.MkdirAll(modelsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(modelsDir, spec.Filename))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	models := ListCachedModels()
	if len(models) == 0 {
		t.Fatal("ListCachedModels returned empty, expected at least one cached model")
	}
	found := false
	for _, m := range models {
		if m.Name == name {
			found = true
			if m.Path == "" {
				t.Errorf("CachedModel.Path is empty for %q", name)
			}
		}
	}
	if !found {
		t.Errorf("expected model %q in ListCachedModels result", name)
	}
}

func TestPing_localFirstBothPresent(t *testing.T) {
	// Covers the default Ping branch: local succeeds, cloud not consulted.
	ts := mockOpenAIServer("")
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_localFirstLocalFailsCloudSucceeds(t *testing.T) {
	// Covers the default Ping branch: local fails, falls through to cloud.
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	cloudServer := mockOpenAIServer("")
	defer cloudServer.Close()

	engine, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_localFirstNoBackends(t *testing.T) {
	// Covers the default Ping branch: neither backend configured.
	engine, err := New(Config{Mode: RouteLocalFirst}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err == nil {
		t.Fatal("expected error when no backends reachable")
	}
}

func TestPing_cloudNonOKStatus(t *testing.T) {
	// Covers the Ping error path in CloudBackend for OpenAI format.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "openai"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Ping(context.Background()); err == nil {
		t.Fatal("expected error for non-OK ping response")
	}
}

func TestComplete_anthropicHTTPError(t *testing.T) {
	// Covers the HTTP error path in completeAnthropic.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer ts.Close()

	engine, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL, Provider: "anthropic"},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for HTTP 429, got nil")
	}
}

func TestComplete_remoteFirstCloudNil(t *testing.T) {
	// Covers completeCloud when cloud is nil (RouteRemoteFirst with no cloud
	// backend configured).
	engine, err := New(Config{Mode: RouteRemoteFirst}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error when cloud backend not configured")
	}
}

// containsPathComponent reports whether any slash-delimited segment of path
// equals component.
func containsPathComponent(path, component string) bool {
	for {
		i := len(path)
		for i > 0 && path[i-1] != '/' {
			i--
		}
		seg := path[i:]
		if seg == component {
			return true
		}
		if i == 0 {
			return false
		}
		path = path[:i-1]
	}
}
