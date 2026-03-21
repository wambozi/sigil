// Package ml provides a multi-backend ML prediction engine that routes
// requests to a local sigil-ml sidecar and/or a cloud ML API.
// It mirrors the internal/inference package pattern.
package ml

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// RoutingMode controls how the engine routes ML requests.
type RoutingMode string

const (
	RouteLocal       RoutingMode = "local"
	RouteLocalFirst  RoutingMode = "localfirst"
	RouteRemoteFirst RoutingMode = "remotefirst"
	RouteRemote      RoutingMode = "remote"
	RouteDisabled    RoutingMode = "disabled"
)

// Prediction is the parsed response from an ML prediction endpoint.
type Prediction struct {
	Endpoint  string         `json:"endpoint"` // e.g. "stuck", "suggest", "duration"
	Result    map[string]any `json:"result"`   // endpoint-specific response
	Routing   string         `json:"routing"`  // "local" or "cloud"
	LatencyMS int64          `json:"latency_ms"`
}

// TrainResult is the response from a training request.
type TrainResult struct {
	Trained    []string `json:"trained"`
	Samples    int      `json:"samples"`
	DurationMS int64    `json:"duration_ms"`
}

// Backend is implemented by each ML backend (local sidecar, cloud API).
type Backend interface {
	Predict(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error)
	Train(ctx context.Context, dbPath string) (*TrainResult, error)
	Ping(ctx context.Context) error
}

// Stoppable is optionally implemented by backends that manage a subprocess.
type Stoppable interface {
	Stop() error
}

// PredictionStore is the optional interface for persisting predictions.
type PredictionStore interface {
	InsertPrediction(ctx context.Context, model, result string, confidence float64, expiresAt *time.Time) error
}

// Engine manages local and cloud ML backends with routing and fallback.
type Engine struct {
	local Backend
	cloud Backend
	mode  RoutingMode
	log   *slog.Logger
	store PredictionStore // optional — set via SetStore
}

// SetStore configures the prediction store for persisting results.
func (e *Engine) SetStore(s PredictionStore) {
	e.store = s
}

// New creates an Engine from the given configuration.
func New(cfg Config, log *slog.Logger) (*Engine, error) {
	if cfg.Mode == RouteDisabled || cfg.Mode == "" {
		return &Engine{mode: RouteDisabled, log: log}, nil
	}

	e := &Engine{
		mode: cfg.Mode,
		log:  log,
	}

	if cfg.Local.Enabled {
		local, err := NewLocal(cfg.Local, log)
		if err != nil {
			log.Warn("ml: local backend unavailable", "err", err)
		} else {
			e.local = local
		}
	}

	if cfg.Cloud.Enabled {
		cloud, err := NewCloud(cfg.Cloud, log)
		if err != nil {
			log.Warn("ml: cloud backend unavailable", "err", err)
		} else {
			e.cloud = cloud
		}
	}

	return e, nil
}

// Predict sends a prediction request to the appropriate backend.
// If a PredictionStore is configured, the result is persisted.
func (e *Engine) Predict(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error) {
	if e.mode == RouteDisabled {
		return nil, fmt.Errorf("ml: disabled")
	}
	var pred *Prediction
	var err error

	switch e.mode {
	case RouteLocal:
		pred, err = e.predictLocal(ctx, endpoint, features)
	case RouteRemote:
		pred, err = e.predictCloud(ctx, endpoint, features)
	case RouteRemoteFirst:
		pred, err = e.predictCloud(ctx, endpoint, features)
		if err != nil && e.local != nil {
			pred, err = e.predictLocal(ctx, endpoint, features)
		}
	default: // localfirst
		pred, err = e.predictLocal(ctx, endpoint, features)
		if err != nil && e.cloud != nil {
			pred, err = e.predictCloud(ctx, endpoint, features)
		}
	}

	// Persist prediction to store if available.
	if err == nil && pred != nil && e.store != nil {
		resultJSON, _ := json.Marshal(pred.Result)
		confidence := 0.0
		if c, ok := pred.Result["confidence"].(float64); ok {
			confidence = c
		} else if c, ok := pred.Result["probability"].(float64); ok {
			confidence = c
		}
		if storeErr := e.store.InsertPrediction(ctx, endpoint, string(resultJSON), confidence, nil); storeErr != nil {
			e.log.Warn("ml: failed to persist prediction", "endpoint", endpoint, "err", storeErr)
		}
	}

	return pred, err
}

// Train triggers model retraining on the appropriate backend.
func (e *Engine) Train(ctx context.Context, dbPath string) (*TrainResult, error) {
	if e.mode == RouteDisabled {
		return nil, fmt.Errorf("ml: disabled")
	}
	// Training always targets local (cloud models are pre-trained).
	if e.local != nil {
		return e.local.Train(ctx, dbPath)
	}
	return nil, fmt.Errorf("ml: no local backend for training")
}

func (e *Engine) predictLocal(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error) {
	if e.local == nil {
		return nil, fmt.Errorf("ml: local backend not configured")
	}
	return e.local.Predict(ctx, endpoint, features)
}

func (e *Engine) predictCloud(ctx context.Context, endpoint string, features map[string]any) (*Prediction, error) {
	if e.cloud == nil {
		return nil, fmt.Errorf("ml: cloud backend not configured")
	}
	return e.cloud.Predict(ctx, endpoint, features)
}

// Ping checks whether any backend matching the routing mode is reachable.
func (e *Engine) Ping(ctx context.Context) error {
	if e.mode == RouteDisabled {
		return fmt.Errorf("ml: disabled")
	}
	switch e.mode {
	case RouteLocal:
		if e.local != nil {
			return e.local.Ping(ctx)
		}
		return fmt.Errorf("ml: local backend not configured")
	case RouteRemote:
		if e.cloud != nil {
			return e.cloud.Ping(ctx)
		}
		return fmt.Errorf("ml: cloud backend not configured")
	default:
		if e.local != nil {
			if err := e.local.Ping(ctx); err == nil {
				return nil
			}
		}
		if e.cloud != nil {
			return e.cloud.Ping(ctx)
		}
		return fmt.Errorf("ml: no backend reachable")
	}
}

// Enabled returns true if the ML engine is not disabled.
func (e *Engine) Enabled() bool {
	return e.mode != RouteDisabled
}

// Close releases resources held by the engine.
func (e *Engine) Close() error {
	if s, ok := e.local.(Stoppable); ok {
		return s.Stop()
	}
	return nil
}
