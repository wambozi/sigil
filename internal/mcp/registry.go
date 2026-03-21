// Package mcp provides the MCP tool registry and tool-calling loop for the
// Sigil workflow assistant.  Tools are backed by store queries and exposed
// to the inference engine for agentic interactions.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolFunc is the signature every registered tool must satisfy.
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// Tool describes a single callable tool that the LLM can invoke.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema for the LLM
	Fn          ToolFunc
}

// Registry holds the set of tools available to the inference engine.
type Registry struct {
	tools []Tool
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools = append(r.tools, t)
}

// Execute finds the named tool and calls it with the provided JSON arguments.
func (r *Registry) Execute(ctx context.Context, name string, argsJSON string) (string, error) {
	for _, t := range r.tools {
		if t.Name == name {
			return t.Fn(ctx, json.RawMessage(argsJSON))
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

// Tools returns all registered tools.
func (r *Registry) Tools() []Tool {
	return r.tools
}
