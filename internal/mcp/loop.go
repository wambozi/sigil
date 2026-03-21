package mcp

import (
	"context"
	"fmt"
	"time"
)

// Message represents a single message in the tool-calling conversation.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents the LLM requesting a tool invocation.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc contains the function name and arguments for a tool call.
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool for the inference engine (OpenAI-compatible schema).
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolDefFunc `json:"function"`
}

// ToolDefFunc holds the name, description, and parameter schema of a tool.
type ToolDefFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolEngineResult is the response from a single inference call.
type ToolEngineResult struct {
	Content   string
	ToolCalls []ToolCall
	Routing   string
	LatencyMS int64
}

// ToolEngine is the interface that the inference engine must satisfy to
// participate in tool-calling loops.
type ToolEngine interface {
	CompleteWithTools(ctx context.Context, messages []Message, tools []ToolDef) (*ToolEngineResult, error)
}

// LoopResult holds the final output of a tool-calling loop.
type LoopResult struct {
	Answer        string
	ToolCallsMade int
	TotalLatencyMS int64
}

const maxToolRounds = 10

const systemPrompt = `You are the Sigil workflow assistant — an AI that helps software engineers understand their work patterns and decide what to do next.

You have access to tools that query the engineer's local workflow data: current task state, ML predictions, GitHub PR status, CI results, quality scores, and more. Use these tools to ground your answers in real data.

Be concise and actionable. Lead with the answer, not the reasoning.`

// ToolDefs returns the OpenAI-compatible tool definitions for all registered tools.
func (r *Registry) ToolDefs() []ToolDef {
	var defs []ToolDef
	for _, t := range r.tools {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

// RunToolLoop drives a multi-turn tool-calling conversation. It sends the
// user query to the engine, executes any requested tools, feeds results back,
// and repeats until the engine produces a final text response or the round
// limit is reached.
func (r *Registry) RunToolLoop(ctx context.Context, engine ToolEngine, query string) (*LoopResult, error) {
	start := time.Now()

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: query},
	}

	defs := r.ToolDefs()
	totalCalls := 0

	for round := 0; round < maxToolRounds; round++ {
		result, err := engine.CompleteWithTools(ctx, messages, defs)
		if err != nil {
			return nil, fmt.Errorf("mcp: engine completion failed (round %d): %w", round, err)
		}

		// If no tool calls, the engine produced a final answer.
		if len(result.ToolCalls) == 0 {
			return &LoopResult{
				Answer:         result.Content,
				ToolCallsMade:  totalCalls,
				TotalLatencyMS: time.Since(start).Milliseconds(),
			}, nil
		}

		// Append the assistant message with tool calls.
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		})

		// Execute each tool call and append results.
		for _, tc := range result.ToolCalls {
			totalCalls++
			output, execErr := r.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				output = fmt.Sprintf(`{"error": %q}`, execErr.Error())
			}
			messages = append(messages, Message{
				Role:       "tool",
				Content:    output,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	// Exhausted rounds — return whatever we have.
	return &LoopResult{
		Answer:         "I was unable to produce a final answer within the tool-call limit. Please try a more specific question.",
		ToolCallsMade:  totalCalls,
		TotalLatencyMS: time.Since(start).Milliseconds(),
	}, nil
}
