// Package inference provides a multi-backend inference engine that routes
// completion requests to local and/or cloud LLM backends based on a
// configurable routing mode.
//
// It replaces the former internal/cactus package, adding support for
// separate local and cloud backends with automatic fallback.
package inference

import (
	"context"
	"fmt"
	"log/slog"
)

// RoutingMode controls how the engine routes inference requests.
type RoutingMode string

const (
	RouteLocal       RoutingMode = "local"       // strictly on-device
	RouteLocalFirst  RoutingMode = "localfirst"  // try local, fall back to cloud
	RouteRemoteFirst RoutingMode = "remotefirst" // prefer cloud, use local if API fails
	RouteRemote      RoutingMode = "remote"      // strictly cloud
)

// CompletionResult is the parsed response from a chat completion call.
type CompletionResult struct {
	Content   string `json:"content"`
	Routing   string `json:"routing"`    // "local" or "cloud"
	LatencyMS int64  `json:"latency_ms"`
}

// Engine manages local and cloud inference backends and routes requests
// according to the configured RoutingMode.
type Engine struct {
	local *LocalBackend
	cloud *CloudBackend
	mode  RoutingMode
	log   *slog.Logger
}

// New creates an Engine from the given configuration. Backend failures during
// initialisation are non-fatal: the engine will use whichever backends are
// available.
func New(cfg Config, log *slog.Logger) (*Engine, error) {
	e := &Engine{
		mode: cfg.Mode,
		log:  log,
	}

	if cfg.Mode == "" {
		e.mode = RouteLocalFirst
	}

	if cfg.Local.Enabled {
		local, err := NewLocal(cfg.Local, log)
		if err != nil {
			log.Warn("inference: local backend unavailable", "err", err)
		} else {
			e.local = local
		}
	}

	if cfg.Cloud.Enabled {
		cloud, err := NewCloud(cfg.Cloud, log)
		if err != nil {
			log.Warn("inference: cloud backend unavailable", "err", err)
		} else {
			e.cloud = cloud
		}
	}

	return e, nil
}

// Complete sends a single-turn prompt and returns the assistant reply.
// Routing and fallback behaviour depend on the configured RoutingMode.
func (e *Engine) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	switch e.mode {
	case RouteLocal:
		return e.completeLocal(ctx, system, user)
	case RouteRemote:
		return e.completeCloud(ctx, system, user)
	case RouteRemoteFirst:
		result, err := e.completeCloud(ctx, system, user)
		if err != nil && e.local != nil {
			e.log.Warn("inference: cloud failed, falling back to local", "err", err)
			return e.completeLocal(ctx, system, user)
		}
		return result, err
	default: // localfirst
		result, err := e.completeLocal(ctx, system, user)
		if err != nil && e.cloud != nil {
			e.log.Warn("inference: local failed, falling back to cloud", "err", err)
			return e.completeCloud(ctx, system, user)
		}
		return result, err
	}
}

func (e *Engine) completeLocal(ctx context.Context, system, user string) (*CompletionResult, error) {
	if e.local == nil {
		return nil, fmt.Errorf("inference: local backend not configured")
	}
	return e.local.Complete(ctx, system, user)
}

func (e *Engine) completeCloud(ctx context.Context, system, user string) (*CompletionResult, error) {
	if e.cloud == nil {
		return nil, fmt.Errorf("inference: cloud backend not configured")
	}
	return e.cloud.Complete(ctx, system, user)
}

// Ping checks whether any backend matching the routing mode is reachable.
func (e *Engine) Ping(ctx context.Context) error {
	switch e.mode {
	case RouteLocal:
		if e.local != nil {
			return e.local.Ping(ctx)
		}
		return fmt.Errorf("inference: local backend not configured")
	case RouteRemote:
		if e.cloud != nil {
			return e.cloud.Ping(ctx)
		}
		return fmt.Errorf("inference: cloud backend not configured")
	default:
		// Try local first, then cloud.
		if e.local != nil {
			if err := e.local.Ping(ctx); err == nil {
				return nil
			}
		}
		if e.cloud != nil {
			return e.cloud.Ping(ctx)
		}
		return fmt.Errorf("inference: no backend reachable")
	}
}

// Close releases resources held by the engine.
func (e *Engine) Close() error {
	if e.local != nil {
		return e.local.Stop()
	}
	return nil
}
