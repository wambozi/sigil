package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HTTP stub server helpers
// These helpers are defined here and used by cloud_test.go, local_test.go,
// and engine_test.go since all test files share the package namespace.
// ---------------------------------------------------------------------------

// mockOpenAIServer returns a test server that speaks the OpenAI chat completions API.
func mockOpenAIServer(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatResponse{ //nolint:errcheck
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

// mockAnthropicServer returns a test server that speaks the Anthropic Messages API.
func mockAnthropicServer(text string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(anthropicResponse{ //nolint:errcheck
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

// mockToolServer returns a test server that responds to tool-calling requests
// with an assistant message that may include tool calls.
func mockToolServer(content string, toolCalls []ChatToolCall) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			resp := chatResponseWithTools{
				Choices: []chatToolChoice{
					{
						Message: struct {
							Role      string         `json:"role"`
							Content   string         `json:"content"`
							ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
						}{
							Role:      "assistant",
							Content:   content,
							ToolCalls: toolCalls,
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp) //nolint:errcheck
		case "/v1/models", "/health":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// localServerWith returns an httptest.Server that handles the given path-to-handler
// mapping, returning 200 OK by default for any unregistered path.
func localServerWith(handlers map[string]http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fn, ok := handlers[r.URL.Path]; ok {
			fn(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

// failingServer returns a test server that always responds HTTP 500.
func failingServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
}

// unavailableServer returns a test server that always responds HTTP 503.
func unavailableServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
}

// rateLimitedServer returns a test server that always responds HTTP 429.
func rateLimitedServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
}

// emptyChoicesServer returns a test server that returns a valid JSON response
// with an empty choices array.
func emptyChoicesServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}}) //nolint:errcheck
	}))
}

// emptyAnthropicServer returns a test server that returns an Anthropic response
// with an empty content array.
func emptyAnthropicServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResponse{Content: []anthropicContent{}}) //nolint:errcheck
	}))
}

// capturingServer captures messages from requests to /v1/chat/completions into
// the provided slice and responds with the given content.
func capturingServer(captured *[]map[string]any, content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				*captured = append(*captured, m.(map[string]any))
			}
		}
		json.NewEncoder(w).Encode(chatResponse{ //nolint:errcheck
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: content}},
			},
		})
	}))
}

// ---------------------------------------------------------------------------
// NewCloud — config defaults and env var
// ---------------------------------------------------------------------------

func TestNewCloud_defaultsOpenAI(t *testing.T) {
	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai"}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "https://api.openai.com", c.baseURL)
	assert.Equal(t, "gpt-4o-mini", c.model)
	assert.Equal(t, "openai", c.format)
}

func TestNewCloud_defaultsAnthropic(t *testing.T) {
	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "anthropic"}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "https://api.anthropic.com", c.baseURL)
	assert.Equal(t, "claude-sonnet-4-20250514", c.model)
	assert.Equal(t, "anthropic", c.format)
}

func TestNewCloud_emptyProviderDefaultsOpenAI(t *testing.T) {
	c, err := NewCloud(CloudConfig{Enabled: true}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "openai", c.format)
}

func TestNewCloud_apiKeyFromEnv(t *testing.T) {
	t.Setenv("SIGIL_CLOUD_API_KEY", "env-key-xyz")

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai"}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "env-key-xyz", c.apiKey)
}

func TestNewCloud_explicitAPIKeyOverridesEnv(t *testing.T) {
	t.Setenv("SIGIL_CLOUD_API_KEY", "env-key-xyz")

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		APIKey:   "explicit-key",
	}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "explicit-key", c.apiKey)
}

func TestNewCloud_customBaseURLAndModel(t *testing.T) {
	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		BaseURL:  "http://localhost:9999",
		Model:    "my-custom-model",
	}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:9999", c.baseURL)
	assert.Equal(t, "my-custom-model", c.model)
}

// ---------------------------------------------------------------------------
// CloudBackend — Complete (completeOpenAI)
// ---------------------------------------------------------------------------

func TestCloudBackend_completeOpenAI_emptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponse{}) //nolint:errcheck
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty choices")
}

func TestCloudBackend_completeOpenAI_decodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not-json") //nolint:errcheck
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestCloudBackend_completeOpenAI_requestError(t *testing.T) {
	ts := mockOpenAIServer("response")
	ts.Close() // close immediately so requests fail at transport level

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}

func TestCloudBackend_completeOpenAI_buildRequestError(t *testing.T) {
	c := &CloudBackend{
		baseURL: "://bad url",
		format:  "openai",
		client:  &http.Client{},
		model:   "gpt-4o-mini",
		log:     testLogger(),
	}
	_, err := c.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CloudBackend — Complete (completeAnthropic)
// ---------------------------------------------------------------------------

func TestCloudBackend_completeAnthropic_decodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not-json") //nolint:errcheck
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "anthropic", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Complete(context.Background(), "", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestCloudBackend_completeAnthropic_requestError(t *testing.T) {
	ts := mockAnthropicServer("response")
	ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "anthropic", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}

func TestCloudBackend_completeAnthropic_buildRequestError(t *testing.T) {
	c := &CloudBackend{
		baseURL: "://bad url",
		format:  "anthropic",
		client:  &http.Client{},
		model:   "claude-sonnet",
		log:     testLogger(),
	}
	_, err := c.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CloudBackend — CompleteWithTools
// ---------------------------------------------------------------------------

func TestCloudBackend_CompleteWithTools_openAI(t *testing.T) {
	wantCall := ChatToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ChatToolCallFunc{
			Name:      "get_weather",
			Arguments: `{"location":"NYC"}`,
		},
	}
	ts := mockToolServer("", []ChatToolCall{wantCall})
	defer ts.Close()

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		BaseURL:  ts.URL,
	}, testLogger())
	require.NoError(t, err)

	msgs := []ChatMessage{{Role: "user", Content: "What is the weather?"}}
	tools := []ChatToolDef{{
		Type: "function",
		Function: ChatToolDefFunc{
			Name:        "get_weather",
			Description: "Get current weather",
			Parameters:  map[string]any{"type": "object"},
		},
	}}

	result, err := c.CompleteWithTools(context.Background(), msgs, tools)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "call_1", result.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", result.ToolCalls[0].Function.Name)
	assert.Equal(t, "cloud", result.Routing)
}

func TestCloudBackend_CompleteWithTools_anthropicUnsupported(t *testing.T) {
	ts := mockAnthropicServer("whatever")
	defer ts.Close()

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "anthropic",
		BaseURL:  ts.URL,
	}, testLogger())
	require.NoError(t, err)

	_, err = c.CompleteWithTools(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported on Anthropic")
}

func TestCloudBackend_CompleteWithTools_httpError(t *testing.T) {
	ts := unavailableServer()
	defer ts.Close()

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		BaseURL:  ts.URL,
	}, testLogger())
	require.NoError(t, err)

	_, err = c.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestCloudBackend_CompleteWithTools_emptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponseWithTools{}) //nolint:errcheck
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		BaseURL:  ts.URL,
	}, testLogger())
	require.NoError(t, err)

	_, err = c.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty choices")
}

func TestCloudBackend_CompleteWithTools_decodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not-json{{{{") //nolint:errcheck
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{
		Enabled:  true,
		Provider: "openai",
		BaseURL:  ts.URL,
	}, testLogger())
	require.NoError(t, err)

	_, err = c.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestCloudBackend_CompleteWithTools_requestError(t *testing.T) {
	ts := mockToolServer("", nil)
	ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.CompleteWithTools(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request")
}

func TestCloudBackend_CompleteWithTools_buildRequestError(t *testing.T) {
	c := &CloudBackend{
		baseURL: "://bad url",
		format:  "openai",
		client:  &http.Client{},
		model:   "gpt-4o-mini",
		log:     testLogger(),
	}
	_, err := c.CompleteWithTools(context.Background(), nil, nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CloudBackend — Ping
// ---------------------------------------------------------------------------

func TestCloudBackend_Ping_anthropic_requestError(t *testing.T) {
	ts := mockAnthropicServer("")
	ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "anthropic", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	err = c.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

func TestCloudBackend_Ping_openAI_requestError(t *testing.T) {
	ts := mockOpenAIServer("")
	ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, Provider: "openai", BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	err = c.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

func TestCloudBackend_Ping_anthropic_badURL(t *testing.T) {
	c := &CloudBackend{
		baseURL: "://bad url",
		format:  "anthropic",
		client:  &http.Client{},
		log:     testLogger(),
	}
	err := c.Ping(context.Background())
	require.Error(t, err)
}

func TestCloudBackend_Ping_openAI_badURL(t *testing.T) {
	c := &CloudBackend{
		baseURL: "://bad url",
		format:  "openai",
		client:  &http.Client{},
		log:     testLogger(),
	}
	err := c.Ping(context.Background())
	require.Error(t, err)
}
