package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	require.NotNil(t, r)
	assert.Empty(t, r.Tools())
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	r.Register(Tool{
		Name:        "tool_a",
		Description: "Tool A",
		Parameters:  map[string]any{"type": "object"},
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "a", nil
		},
	})
	r.Register(Tool{
		Name:        "tool_b",
		Description: "Tool B",
		Parameters:  map[string]any{"type": "object"},
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "b", nil
		},
	})

	tools := r.Tools()
	require.Len(t, tools, 2)
	assert.Equal(t, "tool_a", tools[0].Name)
	assert.Equal(t, "tool_b", tools[1].Name)
}

func TestRegistry_Execute_KnownTool(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "echo",
		Fn: func(_ context.Context, args json.RawMessage) (string, error) {
			return string(args), nil
		},
	})

	got, err := r.Execute(context.Background(), "echo", `{"msg":"hello"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"msg":"hello"}`, got)
}

func TestRegistry_Execute_UnknownTool(t *testing.T) {
	r := NewRegistry()

	_, err := r.Execute(context.Background(), "nonexistent", `{}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: nonexistent")
}

func TestRegistry_Execute_ToolError(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "fail",
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", errors.New("boom")
		},
	})

	_, err := r.Execute(context.Background(), "fail", `{}`)
	require.Error(t, err)
	assert.Equal(t, "boom", err.Error())
}

func TestRegistry_Execute_FirstMatch(t *testing.T) {
	// Registering two tools with different names — only the correct one fires.
	r := NewRegistry()
	r.Register(Tool{
		Name: "alpha",
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "alpha-result", nil
		},
	})
	r.Register(Tool{
		Name: "beta",
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "beta-result", nil
		},
	})

	got, err := r.Execute(context.Background(), "beta", `{}`)
	require.NoError(t, err)
	assert.Equal(t, "beta-result", got)
}

func TestRegistry_Execute_PropagatesContext(t *testing.T) {
	r := NewRegistry()

	type key struct{}
	r.Register(Tool{
		Name: "ctx_check",
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if ctx.Value(key{}) == nil {
				return "", errors.New("context value missing")
			}
			return "ok", nil
		},
	})

	ctx := context.WithValue(context.Background(), key{}, "present")
	got, err := r.Execute(ctx, "ctx_check", `{}`)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
}
