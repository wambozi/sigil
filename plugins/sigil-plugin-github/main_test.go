package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPluginEventJSONSerialization(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	event := PluginEvent{
		Plugin:    "github",
		Kind:      "pr_status",
		Timestamp: now,
		Correlation: map[string]any{
			"repo_root": "/home/user/project",
			"branch":    "feature-x",
			"pr_id":     "42",
		},
		Payload: map[string]any{
			"number": 42,
			"title":  "Add feature X",
			"state":  "open",
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

	if decoded["plugin"] != "github" {
		t.Errorf("expected plugin=github, got %v", decoded["plugin"])
	}
	if decoded["kind"] != "pr_status" {
		t.Errorf("expected kind=pr_status, got %v", decoded["kind"])
	}
}

func TestPluginEventOmitsEmptyCorrelation(t *testing.T) {
	t.Parallel()

	event := PluginEvent{
		Plugin:    "github",
		Kind:      "ci_status",
		Timestamp: time.Now(),
		Payload:   map[string]any{"state": "success"},
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

func TestExtractIssueRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		expected []int
	}{
		{
			name:     "single ref",
			text:     "fix #123",
			expected: []int{123},
		},
		{
			name:     "multiple refs",
			text:     "closes #10, fixes #20, resolves #30",
			expected: []int{10, 20, 30},
		},
		{
			name:     "no refs",
			text:     "this is a normal title",
			expected: nil,
		},
		{
			name:     "ref at start",
			text:     "#99 emergency fix",
			expected: []int{99},
		},
		{
			name:     "ref at end",
			text:     "implement feature #7",
			expected: []int{7},
		},
		{
			name:     "adjacent refs",
			text:     "#1#2#3",
			expected: []int{1, 2, 3},
		},
		{
			name:     "empty string",
			text:     "",
			expected: nil,
		},
		{
			name:     "hash without number",
			text:     "# heading",
			expected: nil,
		},
		{
			name:     "large number",
			text:     "fix #99999",
			expected: []int{99999},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := extractIssueRefs(tc.text)
			if len(result) != len(tc.expected) {
				t.Fatalf("extractIssueRefs(%q) = %v, want %v", tc.text, result, tc.expected)
			}
			for i, v := range result {
				if v != tc.expected[i] {
					t.Errorf("extractIssueRefs(%q)[%d] = %d, want %d", tc.text, i, v, tc.expected[i])
				}
			}
		})
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
			input:    "hello world this is long",
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

func TestMinFunc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-1, 1, -1},
	}

	for _, tc := range tests {
		got := min(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("min(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestGhRepoStruct(t *testing.T) {
	t.Parallel()

	repo := ghRepo{
		LocalPath: "/home/user/project",
		Owner:     "octocat",
		Repo:      "hello-world",
		Remote:    "origin",
		Branch:    "feature-branch",
	}

	if repo.Owner != "octocat" {
		t.Errorf("expected Owner=octocat, got %q", repo.Owner)
	}
	if repo.Repo != "hello-world" {
		t.Errorf("expected Repo=hello-world, got %q", repo.Repo)
	}
}

func TestReadBranchFromGitHEAD(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	// Test with a branch ref.
	headPath := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(headPath, []byte("ref: refs/heads/my-feature\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}

	branch := readBranch(dir)
	if branch != "my-feature" {
		t.Errorf("readBranch() = %q, want %q", branch, "my-feature")
	}

	// Test with a detached HEAD (no ref prefix).
	if err := os.WriteFile(headPath, []byte("abc123def456\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}

	branch = readBranch(dir)
	if branch != "" {
		t.Errorf("readBranch() for detached HEAD = %q, want empty", branch)
	}
}

func TestReadBranchMissingDir(t *testing.T) {
	t.Parallel()

	branch := readBranch("/nonexistent/path")
	if branch != "" {
		t.Errorf("readBranch for missing dir = %q, want empty", branch)
	}
}
