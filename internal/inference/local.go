package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// LocalBackend talks to a local OpenAI-compatible server (e.g. llama-server).
type LocalBackend struct {
	baseURL string
	client  *http.Client
	log     *slog.Logger
}

// NewLocal creates a LocalBackend pointed at the given server URL.
func NewLocal(cfg LocalConfig, log *slog.Logger) (*LocalBackend, error) {
	url := cfg.ServerURL
	if url == "" {
		url = "http://127.0.0.1:8081"
	}

	return &LocalBackend{
		baseURL: url,
		client: &http.Client{
			Timeout: 120 * time.Second, // local inference can be slow on CPU
		},
		log: log,
	}, nil
}

// Complete sends a chat completion request to the local server.
func (l *LocalBackend) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	msgs := make([]chatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: user})

	body, err := json.Marshal(chatRequest{
		Model:    "local",
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

// Ping checks whether the local server is healthy.
func (l *LocalBackend) Ping(ctx context.Context) error {
	url := l.baseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("inference/local: ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inference/local: ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Stop shuts down the local backend. Currently a no-op; issue #4 adds
// subprocess lifecycle management.
func (l *LocalBackend) Stop() error {
	return nil
}
