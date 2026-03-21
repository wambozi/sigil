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
	"time"
)

// CloudBackend talks to a cloud inference API (OpenAI or Anthropic compatible).
type CloudBackend struct {
	baseURL string
	apiKey  string
	model   string
	format  string // "anthropic" or "openai"
	client  *http.Client
	log     *slog.Logger
}

// NewCloud creates a CloudBackend from the given configuration.
func NewCloud(cfg CloudConfig, log *slog.Logger) (*CloudBackend, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("SIGIL_CLOUD_API_KEY")
	}

	format := cfg.Provider
	if format == "" {
		format = "openai"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		switch format {
		case "anthropic":
			baseURL = "https://api.anthropic.com"
		default:
			baseURL = "https://api.openai.com"
		}
	}

	model := cfg.Model
	if model == "" {
		switch format {
		case "anthropic":
			model = "claude-sonnet-4-20250514"
		default:
			model = "gpt-4o-mini"
		}
	}

	return &CloudBackend{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		format:  format,
		client:  &http.Client{Timeout: 60 * time.Second},
		log:     log,
	}, nil
}

// Complete sends a chat completion request to the cloud provider.
func (c *CloudBackend) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	if c.format == "anthropic" {
		return c.completeAnthropic(ctx, system, user)
	}
	return c.completeOpenAI(ctx, system, user)
}

func (c *CloudBackend) completeOpenAI(ctx context.Context, system, user string) (*CompletionResult, error) {
	msgs := make([]chatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: user})

	body, err := json.Marshal(chatRequest{
		Model:    c.model,
		Messages: msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: marshal: %w", err)
	}

	url := c.baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("inference/cloud: HTTP %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("inference/cloud: decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("inference/cloud: empty choices")
	}

	return &CompletionResult{
		Content:   cr.Choices[0].Message.Content,
		Routing:   "cloud",
		LatencyMS: elapsed,
	}, nil
}

// Anthropic Messages API format
type anthropicRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []chatMessage `json:"messages"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
}

func (c *CloudBackend) completeAnthropic(ctx context.Context, system, user string) (*CompletionResult, error) {
	body, err := json.Marshal(anthropicRequest{
		Model:     c.model,
		MaxTokens: 1024,
		System:    system,
		Messages:  []chatMessage{{Role: "user", Content: user}},
	})
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: marshal: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("inference/cloud: HTTP %d: %s", resp.StatusCode, raw)
	}

	var ar anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return nil, fmt.Errorf("inference/cloud: decode: %w", err)
	}
	if len(ar.Content) == 0 {
		return nil, fmt.Errorf("inference/cloud: empty content")
	}

	return &CompletionResult{
		Content:   ar.Content[0].Text,
		Routing:   "cloud",
		LatencyMS: elapsed,
	}, nil
}

// CompleteWithTools sends a tool-calling chat completion request to the cloud provider.
// Tool calling is only supported on OpenAI-compatible providers for now.
func (c *CloudBackend) CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error) {
	if c.format == "anthropic" {
		return nil, fmt.Errorf("inference/cloud: tool calling not supported on Anthropic provider yet")
	}

	type toolRequest struct {
		Model    string        `json:"model"`
		Messages []ChatMessage `json:"messages"`
		Tools    []ChatToolDef `json:"tools,omitempty"`
	}

	body, err := json.Marshal(toolRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: marshal: %w", err)
	}

	url := c.baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference/cloud: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("inference/cloud: HTTP %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponseWithTools
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("inference/cloud: decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("inference/cloud: empty choices")
	}

	return &ToolCompletionResult{
		Content:   cr.Choices[0].Message.Content,
		ToolCalls: cr.Choices[0].Message.ToolCalls,
		Routing:   "cloud",
		LatencyMS: elapsed,
	}, nil
}

// Ping checks whether the cloud provider is reachable.
func (c *CloudBackend) Ping(ctx context.Context) error {
	if c.format == "anthropic" {
		// Anthropic doesn't have a /models endpoint; just check connectivity.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
		if err != nil {
			return err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("inference/cloud: ping: %w", err)
		}
		resp.Body.Close()
		return nil // Any response means the server is reachable
	}

	url := c.baseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("inference/cloud: ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inference/cloud: ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}
