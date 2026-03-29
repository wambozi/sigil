package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPluginEventJSONSerialization(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	event := PluginEvent{
		Plugin:    "claude",
		Kind:      "tool_use",
		Timestamp: now,
		Correlation: map[string]any{
			"repo_root": "/home/user/project",
		},
		Payload: map[string]any{
			"tool_name":  "Read",
			"session_id": "abc-123",
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal PluginEvent: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal PluginEvent: %v", err)
	}

	if decoded["plugin"] != "claude" {
		t.Errorf("expected plugin=claude, got %v", decoded["plugin"])
	}
	if decoded["kind"] != "tool_use" {
		t.Errorf("expected kind=tool_use, got %v", decoded["kind"])
	}

	payload, ok := decoded["payload"].(map[string]any)
	if !ok {
		t.Fatal("payload is not a map")
	}
	if payload["tool_name"] != "Read" {
		t.Errorf("expected tool_name=Read, got %v", payload["tool_name"])
	}

	corr, ok := decoded["correlation"].(map[string]any)
	if !ok {
		t.Fatal("correlation is not a map")
	}
	if corr["repo_root"] != "/home/user/project" {
		t.Errorf("expected repo_root=/home/user/project, got %v", corr["repo_root"])
	}
}

func TestPluginEventOmitsEmptyCorrelation(t *testing.T) {
	t.Parallel()

	event := PluginEvent{
		Plugin:    "claude",
		Kind:      "raw_input",
		Timestamp: time.Now(),
		Payload:   map[string]any{"raw": "hello"},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, exists := decoded["correlation"]; exists {
		t.Error("expected correlation to be omitted when nil, but it was present")
	}
}

func TestHookInputJSONRoundTrip(t *testing.T) {
	t.Parallel()

	input := HookInput{
		SessionID: "session-1",
		ToolName:  "Bash",
		ToolInput: map[string]any{
			"command": "git status",
		},
		ToolOutput: "On branch main",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal HookInput: %v", err)
	}

	var decoded HookInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal HookInput: %v", err)
	}

	if decoded.SessionID != "session-1" {
		t.Errorf("expected SessionID=session-1, got %q", decoded.SessionID)
	}
	if decoded.ToolName != "Bash" {
		t.Errorf("expected ToolName=Bash, got %q", decoded.ToolName)
	}
}

func TestHookInputOmitsEmptyFields(t *testing.T) {
	t.Parallel()

	input := HookInput{
		SessionID: "s1",
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/tmp/foo"},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, exists := decoded["tool_output"]; exists {
		t.Error("expected tool_output to be omitted when nil")
	}
	if _, exists := decoded["error"]; exists {
		t.Error("expected error to be omitted when empty")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		n        int
		expected string
	}{
		{
			name:     "short string unchanged",
			input:    "hello",
			n:        10,
			expected: "hello",
		},
		{
			name:     "exact length unchanged",
			input:    "hello",
			n:        5,
			expected: "hello",
		},
		{
			name:     "long string truncated",
			input:    "hello world this is a long string",
			n:        10,
			expected: "hello worl...",
		},
		{
			name:     "empty string",
			input:    "",
			n:        5,
			expected: "",
		},
		{
			name:     "truncate to 1",
			input:    "abc",
			n:        1,
			expected: "a...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := truncate(tc.input, tc.n)
			if result != tc.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.n, result, tc.expected)
			}
		})
	}
}

func TestAddHookNewHooks(t *testing.T) {
	t.Parallel()

	hooks := make(map[string]any)
	entry := map[string]any{
		"type":    "command",
		"command": "/usr/local/bin/sigil-plugin-claude",
	}

	addHook(hooks, "PostToolUse", entry)

	hookList, ok := hooks["PostToolUse"].([]any)
	if !ok {
		t.Fatal("expected PostToolUse to be []any")
	}
	if len(hookList) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hookList))
	}
}

func TestAddHookAlreadyInstalled(t *testing.T) {
	t.Parallel()

	hooks := map[string]any{
		"PostToolUse": []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "/usr/local/bin/sigil-plugin-claude",
					},
				},
			},
		},
	}
	entry := map[string]any{
		"type":    "command",
		"command": "/some/other/path/sigil-plugin-claude",
	}

	addHook(hooks, "PostToolUse", entry)

	hookList := hooks["PostToolUse"].([]any)
	if len(hookList) != 1 {
		t.Errorf("expected no duplicate, got %d entries", len(hookList))
	}
}

func TestAddHookAppendsToExisting(t *testing.T) {
	t.Parallel()

	hooks := map[string]any{
		"PostToolUse": []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "/usr/local/bin/other-plugin",
					},
				},
			},
		},
	}
	entry := map[string]any{
		"type":    "command",
		"command": "/usr/local/bin/sigil-plugin-claude",
	}

	addHook(hooks, "PostToolUse", entry)

	hookList := hooks["PostToolUse"].([]any)
	if len(hookList) != 2 {
		t.Errorf("expected 2 entries after append, got %d", len(hookList))
	}
}
