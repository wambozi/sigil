package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock ToolEngine ---------------------------------------------------------

// stubEngine implements ToolEngine with a queue of canned responses.
type stubEngine struct {
	responses []*ToolEngineResult
	errors    []error
	calls     int
}

func (s *stubEngine) CompleteWithTools(_ context.Context, _ []Message, _ []ToolDef) (*ToolEngineResult, error) {
	i := s.calls
	s.calls++
	if i < len(s.errors) && s.errors[i] != nil {
		return nil, s.errors[i]
	}
	if i < len(s.responses) {
		return s.responses[i], nil
	}
	// Default: final answer with no tool calls.
	return &ToolEngineResult{Content: "default answer"}, nil
}

// --- ToolDefs ----------------------------------------------------------------

func TestRegistry_ToolDefs_Empty(t *testing.T) {
	r := NewRegistry()
	assert.Nil(t, r.ToolDefs())
}

func TestRegistry_ToolDefs_Shape(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name:        "my_tool",
		Description: "does a thing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn:          func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	})

	defs := r.ToolDefs()
	require.Len(t, defs, 1)
	assert.Equal(t, "function", defs[0].Type)
	assert.Equal(t, "my_tool", defs[0].Function.Name)
	assert.Equal(t, "does a thing", defs[0].Function.Description)
	assert.NotNil(t, defs[0].Function.Parameters)
}

func TestRegistry_ToolDefs_MultipleTools(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("tool_%d", i)
		r.Register(Tool{
			Name: name,
			Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
		})
	}

	defs := r.ToolDefs()
	require.Len(t, defs, 5)
	for i, d := range defs {
		assert.Equal(t, fmt.Sprintf("tool_%d", i), d.Function.Name)
		assert.Equal(t, "function", d.Type)
	}
}

// --- RunToolLoop — happy path ------------------------------------------------

func TestRunToolLoop_ImmediateFinalAnswer(t *testing.T) {
	r := NewRegistry()
	engine := &stubEngine{
		responses: []*ToolEngineResult{
			{Content: "here is your answer"},
		},
	}

	result, err := r.RunToolLoop(context.Background(), engine, "what should I do?")
	require.NoError(t, err)
	assert.Equal(t, "here is your answer", result.Answer)
	assert.Equal(t, 0, result.ToolCallsMade)
	assert.GreaterOrEqual(t, result.TotalLatencyMS, int64(0))
}

func TestRunToolLoop_SingleToolCallThenAnswer(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "lookup",
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"value": 42}`, nil
		},
	})

	engine := &stubEngine{
		responses: []*ToolEngineResult{
			{
				Content: "",
				ToolCalls: []ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: ToolCallFunc{
							Name:      "lookup",
							Arguments: `{}`,
						},
					},
				},
			},
			{Content: "the answer is 42"},
		},
	}

	result, err := r.RunToolLoop(context.Background(), engine, "what is the value?")
	require.NoError(t, err)
	assert.Equal(t, "the answer is 42", result.Answer)
	assert.Equal(t, 1, result.ToolCallsMade)
}

func TestRunToolLoop_MultipleToolCallsInOneRound(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "tool_x",
		Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return `"x"`, nil },
	})
	r.Register(Tool{
		Name: "tool_y",
		Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return `"y"`, nil },
	})

	engine := &stubEngine{
		responses: []*ToolEngineResult{
			{
				ToolCalls: []ToolCall{
					{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "tool_x", Arguments: `{}`}},
					{ID: "c2", Type: "function", Function: ToolCallFunc{Name: "tool_y", Arguments: `{}`}},
				},
			},
			{Content: "done"},
		},
	}

	result, err := r.RunToolLoop(context.Background(), engine, "query")
	require.NoError(t, err)
	assert.Equal(t, "done", result.Answer)
	assert.Equal(t, 2, result.ToolCallsMade)
}

func TestRunToolLoop_ToolCallUnknownToolGivesErrorJSON(t *testing.T) {
	// Unknown tool → error JSON is fed back as tool result; loop continues.
	r := NewRegistry()

	var capturedMessages []Message
	engine := &engineCapture{
		responses: []*ToolEngineResult{
			{
				ToolCalls: []ToolCall{
					{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "ghost_tool", Arguments: `{}`}},
				},
			},
			{Content: "handled it"},
		},
		capture: &capturedMessages,
	}

	result, err := r.RunToolLoop(context.Background(), engine, "go")
	require.NoError(t, err)
	assert.Equal(t, "handled it", result.Answer)
	assert.Equal(t, 1, result.ToolCallsMade)

	// Verify the tool result message contains the error JSON.
	found := false
	for _, m := range capturedMessages {
		if m.Role == "tool" && m.Name == "ghost_tool" {
			assert.Contains(t, m.Content, "unknown tool")
			found = true
		}
	}
	assert.True(t, found, "expected a tool result message for ghost_tool")
}

// engineCapture records all messages passed to CompleteWithTools.
type engineCapture struct {
	responses []*ToolEngineResult
	capture   *[]Message
	calls     int
}

func (e *engineCapture) CompleteWithTools(_ context.Context, messages []Message, _ []ToolDef) (*ToolEngineResult, error) {
	*e.capture = append(*e.capture, messages...)
	i := e.calls
	e.calls++
	if i < len(e.responses) {
		return e.responses[i], nil
	}
	return &ToolEngineResult{Content: "fallback"}, nil
}

// --- RunToolLoop — round limit -----------------------------------------------

func TestRunToolLoop_ExhaustsRoundLimit(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "infinite",
		Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return `{}`, nil },
	})

	// Engine always requests a tool call — never produces a final answer.
	engine := &alwaysCallEngine{toolName: "infinite"}

	result, err := r.RunToolLoop(context.Background(), engine, "loop forever")
	require.NoError(t, err)
	assert.Equal(t, maxToolRounds, result.ToolCallsMade)
	assert.Contains(t, result.Answer, "unable to produce a final answer")
}

// alwaysCallEngine always returns a tool call request.
type alwaysCallEngine struct {
	toolName string
}

func (e *alwaysCallEngine) CompleteWithTools(_ context.Context, _ []Message, _ []ToolDef) (*ToolEngineResult, error) {
	return &ToolEngineResult{
		ToolCalls: []ToolCall{
			{ID: "loop", Type: "function", Function: ToolCallFunc{Name: e.toolName, Arguments: `{}`}},
		},
	}, nil
}

// --- RunToolLoop — engine errors ---------------------------------------------

func TestRunToolLoop_EngineErrorOnFirstRound(t *testing.T) {
	r := NewRegistry()
	engine := &stubEngine{
		errors: []error{errors.New("inference offline")},
	}

	_, err := r.RunToolLoop(context.Background(), engine, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inference offline")
	assert.Contains(t, err.Error(), "round 0")
}

func TestRunToolLoop_EngineErrorAfterToolCall(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "ok_tool",
		Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return `{}`, nil },
	})

	engine := &stubEngine{
		responses: []*ToolEngineResult{
			{
				ToolCalls: []ToolCall{
					{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "ok_tool", Arguments: `{}`}},
				},
			},
		},
		errors: []error{nil, errors.New("timed out")},
	}

	_, err := r.RunToolLoop(context.Background(), engine, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "round 1")
}

// --- RunToolLoop — context cancellation -------------------------------------

func TestRunToolLoop_ContextCancelled(t *testing.T) {
	r := NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before we even start

	engine := &stubEngine{
		errors: []error{context.Canceled},
	}

	_, err := r.RunToolLoop(ctx, engine, "query")
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

// --- RunToolLoop — message construction -------------------------------------

func TestRunToolLoop_MessagesIncludeSystemAndUser(t *testing.T) {
	r := NewRegistry()

	var firstMessages []Message
	engine := &messageRecordEngine{
		onCall: func(msgs []Message) {
			if firstMessages == nil {
				firstMessages = append([]Message(nil), msgs...)
			}
		},
		result: &ToolEngineResult{Content: "ok"},
	}

	_, err := r.RunToolLoop(context.Background(), engine, "my question")
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(firstMessages), 2)
	assert.Equal(t, "system", firstMessages[0].Role)
	assert.Equal(t, "user", firstMessages[1].Role)
	assert.Equal(t, "my question", firstMessages[1].Content)
}

// messageRecordEngine calls onCall with the messages on each invocation.
type messageRecordEngine struct {
	onCall func([]Message)
	result *ToolEngineResult
}

func (e *messageRecordEngine) CompleteWithTools(_ context.Context, msgs []Message, _ []ToolDef) (*ToolEngineResult, error) {
	e.onCall(msgs)
	return e.result, nil
}

// --- RunToolLoop — tool result message shape --------------------------------

func TestRunToolLoop_ToolResultMessageShape(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name: "data",
		Fn:   func(_ context.Context, _ json.RawMessage) (string, error) { return `{"x":1}`, nil },
	})

	seq := &sequentialEngine{
		steps: []*ToolEngineResult{
			{
				ToolCalls: []ToolCall{
					{ID: "tc-99", Type: "function", Function: ToolCallFunc{Name: "data", Arguments: `{}`}},
				},
			},
			{Content: "final"},
		},
	}

	result, err := r.RunToolLoop(context.Background(), seq, "check shape")
	require.NoError(t, err)
	assert.Equal(t, "final", result.Answer)

	msgs := seq.capturedMessages
	// Find the tool result message.
	var toolMsg *Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			toolMsg = &msgs[i]
			break
		}
	}
	require.NotNil(t, toolMsg, "expected a tool role message")
	assert.Equal(t, "tc-99", toolMsg.ToolCallID)
	assert.Equal(t, "data", toolMsg.Name)
	assert.Equal(t, `{"x":1}`, toolMsg.Content)
}

// sequentialEngine returns responses in order, capturing all messages.
type sequentialEngine struct {
	steps            []*ToolEngineResult
	capturedMessages []Message
	call             int
}

func (e *sequentialEngine) CompleteWithTools(_ context.Context, msgs []Message, _ []ToolDef) (*ToolEngineResult, error) {
	e.capturedMessages = append(e.capturedMessages, msgs...)
	i := e.call
	e.call++
	if i < len(e.steps) {
		return e.steps[i], nil
	}
	return &ToolEngineResult{Content: "fallback"}, nil
}

// --- LoopResult fields -------------------------------------------------------

func TestLoopResult_LatencyIsNonNegative(t *testing.T) {
	r := NewRegistry()
	engine := &stubEngine{
		responses: []*ToolEngineResult{{Content: "answer"}},
	}

	result, err := r.RunToolLoop(context.Background(), engine, "timing test")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.TotalLatencyMS, int64(0))
}
