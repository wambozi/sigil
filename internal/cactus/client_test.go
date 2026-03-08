package cactus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockChatResponse builds a minimal OpenAI-compatible chat response body.
func mockChatResponse(content, routingDecision string, latencyMS int64) []byte {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Message msg `json:"message"`
	}
	type routing struct {
		Decision  string `json:"decision"`
		LatencyMS int64  `json:"latency_ms"`
	}
	type resp struct {
		Choices       []choice `json:"choices"`
		CactusRouting routing  `json:"cactus_routing,omitempty"`
	}
	b, _ := json.Marshal(resp{
		Choices:       []choice{{Message: msg{Role: "assistant", Content: content}}},
		CactusRouting: routing{Decision: routingDecision, LatencyMS: latencyMS},
	})
	return b
}

func TestComplete_happyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Cactus-Routing") != "local" {
			t.Errorf("routing header: got %q, want %q", r.Header.Get("X-Cactus-Routing"), "local")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(mockChatResponse("you edited 3 files", "local", 45))
	}))
	defer ts.Close()

	c := New(ts.URL, "test-model", RouteLocal)
	result, err := c.Complete(context.Background(), "you are sigild", "summarise activity")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Content != "you edited 3 files" {
		t.Errorf("Content: got %q", result.Content)
	}
	if result.Routing != "local" {
		t.Errorf("Routing: got %q, want %q", result.Routing, "local")
	}
}

func TestComplete_nonCactusBackend_defaultsToCloud(t *testing.T) {
	// Backend returns no cactus_routing field — should default to "cloud".
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type choice struct {
			Message msg `json:"message"`
		}
		type resp struct {
			Choices []choice `json:"choices"`
		}
		json.NewEncoder(w).Encode(resp{
			Choices: []choice{{Message: msg{Role: "assistant", Content: "hello"}}},
		})
	}))
	defer ts.Close()

	c := New(ts.URL, "gpt-4", RouteRemote)
	result, err := c.Complete(context.Background(), "", "hello")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Routing != "cloud" {
		t.Errorf("Routing: got %q, want %q (non-Cactus default)", result.Routing, "cloud")
	}
}

func TestComplete_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := New(ts.URL, "model", RouteLocal)
	_, err := c.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestComplete_emptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer ts.Close()

	c := New(ts.URL, "model", RouteLocal)
	_, err := c.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
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
		w.Write(mockChatResponse("ok", "local", 10))
	}))
	defer ts.Close()

	c := New(ts.URL, "model", RouteLocal)
	// Empty system prompt — should send only the user message.
	c.Complete(context.Background(), "", "user only")

	if len(capturedMessages) != 1 {
		t.Errorf("expected 1 message (no system), got %d", len(capturedMessages))
	}
	if capturedMessages[0]["role"] != "user" {
		t.Errorf("expected user role, got %q", capturedMessages[0]["role"])
	}
}

func TestPing_up(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(ts.URL, "model", RouteLocal)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_nonOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := New(ts.URL, "model", RouteLocal)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error for HTTP 503, got nil")
	}
}
