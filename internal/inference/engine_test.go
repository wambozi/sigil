package inference

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
