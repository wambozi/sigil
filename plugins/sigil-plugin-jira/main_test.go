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
		Plugin:    "jira",
		Kind:      "story",
		Timestamp: now,
		Correlation: map[string]any{
			"story_id": "PROJ-123",
		},
		Payload: map[string]any{
			"key":     "PROJ-123",
			"summary": "Implement feature X",
			"status":  "In Progress",
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal PluginEvent: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["plugin"] != "jira" {
		t.Errorf("expected plugin=jira, got %v", decoded["plugin"])
	}
	if decoded["kind"] != "story" {
		t.Errorf("expected kind=story, got %v", decoded["kind"])
	}

	corr := decoded["correlation"].(map[string]any)
	if corr["story_id"] != "PROJ-123" {
		t.Errorf("expected story_id=PROJ-123, got %v", corr["story_id"])
	}
}

func TestPluginEventOmitsEmptyCorrelation(t *testing.T) {
	t.Parallel()

	event := PluginEvent{
		Plugin:    "jira",
		Kind:      "sprint",
		Timestamp: time.Now(),
		Payload:   map[string]any{"sprint": "Sprint 5"},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	json.Unmarshal(data, &decoded)

	if _, exists := decoded["correlation"]; exists {
		t.Error("expected correlation to be omitted when nil")
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

func TestExtractAcceptanceCriteria(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		desc     string
		expected []string
	}{
		{
			name:     "empty description",
			desc:     "",
			expected: nil,
		},
		{
			name: "with acceptance criteria section",
			desc: `Some intro text

## Acceptance Criteria
- [ ] User can log in
- [ ] User can log out
- [x] Admin dashboard loads

More text here`,
			expected: []string{
				"User can log in",
				"User can log out",
				"Admin dashboard loads",
			},
		},
		{
			name: "with definition of done",
			desc: `## Definition of Done
* Tests pass
* Code reviewed`,
			expected: []string{
				"Tests pass",
				"Code reviewed",
			},
		},
		{
			name:     "no AC section",
			desc:     "This is just a description\nwith some lines\nbut no criteria",
			expected: nil,
		},
		{
			name: "criteria keyword in header",
			desc: `# Criteria
1. First item
2. Second item`,
			expected: []string{
				"First item",
				"Second item",
			},
		},
		{
			name: "AC with blank line before items",
			desc: `## Acceptance Criteria

- Item one
- Item two`,
			expected: []string{
				"Item one",
				"Item two",
			},
		},
		{
			name: "AC section ends at blank line",
			desc: `## Acceptance
- First
- Second

## Other Section
- Not AC`,
			expected: []string{
				"First",
				"Second",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := extractAcceptanceCriteria(tc.desc)
			if len(result) != len(tc.expected) {
				t.Fatalf("extractAcceptanceCriteria() = %v (len %d), want %v (len %d)",
					result, len(result), tc.expected, len(tc.expected))
			}
			for i, v := range result {
				if v != tc.expected[i] {
					t.Errorf("item[%d] = %q, want %q", i, v, tc.expected[i])
				}
			}
		})
	}
}

func TestExtractPlainText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text passthrough",
			input:    "This is plain text",
			expected: "This is plain text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name: "ADF JSON with text nodes",
			input: `{
				"type": "doc",
				"content": [
					{
						"type": "paragraph",
						"content": [
							{"type": "text", "text": "Hello"},
							{"type": "text", "text": "world"}
						]
					}
				]
			}`,
			expected: "Hello world",
		},
		{
			name: "ADF JSON with multiple paragraphs",
			input: `{
				"type": "doc",
				"content": [
					{
						"type": "paragraph",
						"content": [
							{"type": "text", "text": "First"}
						]
					},
					{
						"type": "paragraph",
						"content": [
							{"type": "text", "text": "Second"}
						]
					}
				]
			}`,
			expected: "First Second",
		},
		{
			name:     "invalid JSON falls back to raw",
			input:    "{not valid json",
			expected: "{not valid json",
		},
		{
			name: "ADF with no text nodes falls back to raw",
			input: `{
				"type": "doc",
				"content": [
					{
						"type": "paragraph",
						"content": []
					}
				]
			}`,
			expected: `{
				"type": "doc",
				"content": [
					{
						"type": "paragraph",
						"content": []
					}
				]
			}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := extractPlainText(tc.input)
			if result != tc.expected {
				t.Errorf("extractPlainText() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestJiraSearchResultUnmarshal(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"issues": [
			{
				"key": "PROJ-42",
				"fields": {
					"summary": "Fix the widget",
					"status": {"name": "In Progress"},
					"priority": {"name": "High"},
					"issuetype": {"name": "Story"},
					"assignee": {
						"displayName": "Jane Doe",
						"emailAddress": "jane@example.com"
					},
					"reporter": {"displayName": "John Smith"},
					"labels": ["backend", "urgent"],
					"created": "2026-01-15T10:00:00.000+0000",
					"updated": "2026-03-20T14:30:00.000+0000"
				}
			}
		],
		"total": 1
	}`

	var result jiraSearchResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("expected total=1, got %d", result.Total)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result.Issues))
	}

	issue := result.Issues[0]
	if issue.Key != "PROJ-42" {
		t.Errorf("expected key=PROJ-42, got %q", issue.Key)
	}
	if issue.Fields.Summary != "Fix the widget" {
		t.Errorf("expected summary='Fix the widget', got %q", issue.Fields.Summary)
	}
	if issue.Fields.Status.Name != "In Progress" {
		t.Errorf("expected status=In Progress, got %q", issue.Fields.Status.Name)
	}
	if issue.Fields.Priority.Name != "High" {
		t.Errorf("expected priority=High, got %q", issue.Fields.Priority.Name)
	}
	if len(issue.Fields.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(issue.Fields.Labels))
	}
}

func TestJiraIssueWithOptionalFields(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"key": "PROJ-99",
		"fields": {
			"summary": "With parent and sprint",
			"status": {"name": "Done"},
			"priority": {"name": "Low"},
			"issuetype": {"name": "Task"},
			"assignee": {"displayName": "Alice"},
			"reporter": {"displayName": "Bob"},
			"sprint": {
				"name": "Sprint 5",
				"state": "active",
				"goal": "Deliver features"
			},
			"story_points": 5.0,
			"parent": {
				"key": "PROJ-10",
				"fields": {"summary": "Epic: Big Feature"}
			},
			"subtasks": [
				{
					"key": "PROJ-100",
					"fields": {
						"summary": "Subtask 1",
						"status": {"name": "To Do"}
					}
				}
			],
			"labels": [],
			"created": "2026-01-01T00:00:00.000+0000",
			"updated": "2026-03-27T00:00:00.000+0000"
		}
	}`

	var issue jiraIssue
	if err := json.Unmarshal([]byte(jsonStr), &issue); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if issue.Fields.Sprint == nil {
		t.Fatal("expected sprint to be non-nil")
	}
	if issue.Fields.Sprint.Name != "Sprint 5" {
		t.Errorf("expected sprint name=Sprint 5, got %q", issue.Fields.Sprint.Name)
	}

	if issue.Fields.StoryPoints == nil {
		t.Fatal("expected story_points to be non-nil")
	}
	if *issue.Fields.StoryPoints != 5.0 {
		t.Errorf("expected story_points=5.0, got %f", *issue.Fields.StoryPoints)
	}

	if issue.Fields.Parent == nil {
		t.Fatal("expected parent to be non-nil")
	}
	if issue.Fields.Parent.Key != "PROJ-10" {
		t.Errorf("expected parent key=PROJ-10, got %q", issue.Fields.Parent.Key)
	}

	if len(issue.Fields.Subtasks) != 1 {
		t.Fatalf("expected 1 subtask, got %d", len(issue.Fields.Subtasks))
	}
	if issue.Fields.Subtasks[0].Key != "PROJ-100" {
		t.Errorf("expected subtask key=PROJ-100, got %q", issue.Fields.Subtasks[0].Key)
	}
}
