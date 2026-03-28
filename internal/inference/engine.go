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
	Routing   string `json:"routing"` // "local" or "cloud"
	LatencyMS int64  `json:"latency_ms"`
}

// ChatMessage is the multi-turn message format for tool-calling completions.
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// ChatToolCall represents a tool call issued by the model.
type ChatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ChatToolCallFunc `json:"function"`
}

// ChatToolCallFunc holds the function name and JSON-encoded arguments.
type ChatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatToolDef describes a tool the model may call.
type ChatToolDef struct {
	Type     string          `json:"type"`
	Function ChatToolDefFunc `json:"function"`
}

// ChatToolDefFunc holds the tool function schema.
type ChatToolDefFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCompletionResult extends CompletionResult with tool call support.
type ToolCompletionResult struct {
	Content   string         `json:"content"`
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
	Routing   string         `json:"routing"`
	LatencyMS int64          `json:"latency_ms"`
}

// Backend is implemented by each inference backend (local, cloud).
type Backend interface {
	Complete(ctx context.Context, system, user string) (*CompletionResult, error)
	Ping(ctx context.Context) error
}

// ToolBackend is implemented by backends that support tool-calling completions.
type ToolBackend interface {
	CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error)
}

// Compile-time assertions: both backends satisfy Backend and ToolBackend.
var (
	_ Backend     = (*LocalBackend)(nil)
	_ Backend     = (*CloudBackend)(nil)
	_ ToolBackend = (*LocalBackend)(nil)
	_ ToolBackend = (*CloudBackend)(nil)
)

// Engine manages local and cloud inference backends and routes requests
// according to the configured RoutingMode.
type Engine struct {
	local Backend
	cloud Backend
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

// CompleteWithTools sends a multi-turn tool-calling request and returns the
// assistant reply, which may include tool calls. Routing and fallback
// behaviour match Complete().
func (e *Engine) CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error) {
	switch e.mode {
	case RouteLocal:
		return e.completeWithToolsLocal(ctx, messages, tools)
	case RouteRemote:
		return e.completeWithToolsCloud(ctx, messages, tools)
	case RouteRemoteFirst:
		result, err := e.completeWithToolsCloud(ctx, messages, tools)
		if err != nil && e.local != nil {
			e.log.Warn("inference: cloud failed, falling back to local", "err", err)
			return e.completeWithToolsLocal(ctx, messages, tools)
		}
		return result, err
	default: // localfirst
		result, err := e.completeWithToolsLocal(ctx, messages, tools)
		if err != nil && e.cloud != nil {
			e.log.Warn("inference: local failed, falling back to cloud", "err", err)
			return e.completeWithToolsCloud(ctx, messages, tools)
		}
		return result, err
	}
}

func (e *Engine) completeWithToolsLocal(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error) {
	if e.local == nil {
		return nil, fmt.Errorf("inference: local backend not configured")
	}
	tb, ok := e.local.(ToolBackend)
	if !ok {
		return nil, fmt.Errorf("inference: local backend does not support tool calling")
	}
	return tb.CompleteWithTools(ctx, messages, tools)
}

func (e *Engine) completeWithToolsCloud(ctx context.Context, messages []ChatMessage, tools []ChatToolDef) (*ToolCompletionResult, error) {
	if e.cloud == nil {
		return nil, fmt.Errorf("inference: cloud backend not configured")
	}
	tb, ok := e.cloud.(ToolBackend)
	if !ok {
		return nil, fmt.Errorf("inference: cloud backend does not support tool calling")
	}
	return tb.CompleteWithTools(ctx, messages, tools)
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

// Stoppable is optionally implemented by backends that manage a subprocess.
type Stoppable interface {
	Stop() error
}

// localInfoProvider is satisfied by *LocalBackend for extracting process info.
type localInfoProvider interface {
	ProcessInfo() (pid int, managed bool, ok bool)
	ModelName() string
	CtxSize() int
}

// LocalProcessInfo returns llama-server process info if a local backend is configured.
// ok is false if no local backend exists or no process is running.
func (e *Engine) LocalProcessInfo() (pid int, managed bool, ok bool) {
	if p, yes := e.local.(localInfoProvider); yes {
		return p.ProcessInfo()
	}
	return 0, false, false
}

// LocalModelName returns the configured local model name, or empty string.
func (e *Engine) LocalModelName() string {
	if p, ok := e.local.(localInfoProvider); ok {
		return p.ModelName()
	}
	return ""
}

// LocalCtxSize returns the configured context window size, or 0 if no local backend.
func (e *Engine) LocalCtxSize() int {
	if p, ok := e.local.(localInfoProvider); ok {
		return p.CtxSize()
	}
	return 0
}

// Close releases resources held by the engine. If the local backend is a
// managed subprocess, it is stopped.
func (e *Engine) Close() error {
	if s, ok := e.local.(Stoppable); ok {
		return s.Stop()
	}
	return nil
}
