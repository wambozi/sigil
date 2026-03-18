package ml

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

// CloudBackend talks to a hosted sigil-ml API.
type CloudBackend struct {
	baseURL string
	apiKey  string
	client  *http.Client
	log     *slog.Logger
}

// NewCloud creates a CloudBackend.
func NewCloud(cfg CloudConfig, log *slog.Logger) (*CloudBackend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("ml/cloud: base_url is required")
	}
	return &CloudBackend{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}, nil
}

// Predict calls a prediction endpoint on the cloud server.
func (c *CloudBackend) Predict(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error) {
	body, err := json.Marshal(map[string]any{"features": features})
	if err != nil {
		return nil, err
	}

	url := c.baseURL + "/predict/" + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("ml/cloud: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ml/cloud: %s: HTTP %d: %s", endpoint, resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ml/cloud: %s: decode: %w", endpoint, err)
	}

	return &Prediction{
		Endpoint:  endpoint,
		Result:    result,
		Routing:   "cloud",
		LatencyMS: latency,
	}, nil
}

// Train is not supported on cloud — cloud models are pre-trained.
func (c *CloudBackend) Train(_ context.Context, _ string) (*TrainResult, error) {
	return nil, fmt.Errorf("ml/cloud: training not supported on cloud backend")
}

// Ping checks if the cloud server is healthy.
func (c *CloudBackend) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml/cloud: health check returned %d", resp.StatusCode)
	}
	return nil
}
