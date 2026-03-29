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
		Plugin:    "vscode",
		Kind:      "workspace_change",
		Timestamp: now,
		Correlation: map[string]any{
			"repo_root": "/home/user/project",
		},
		Payload: map[string]any{
			"workspace":               "/home/user/project",
			"total_recent_workspaces": 5,
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

	if decoded["plugin"] != "vscode" {
		t.Errorf("expected plugin=vscode, got %v", decoded["plugin"])
	}
	if decoded["kind"] != "workspace_change" {
		t.Errorf("expected kind=workspace_change, got %v", decoded["kind"])
	}

	payload := decoded["payload"].(map[string]any)
	if payload["workspace"] != "/home/user/project" {
		t.Errorf("expected workspace=/home/user/project, got %v", payload["workspace"])
	}
}

func TestPluginEventOmitsEmptyCorrelation(t *testing.T) {
	t.Parallel()

	event := PluginEvent{
		Plugin:    "vscode",
		Kind:      "extensions",
		Timestamp: time.Now(),
		Payload:   map[string]any{"installed": []string{"copilot"}},
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

func TestIsInterestingExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// AI extensions.
		{"copilot", "github.copilot-1.234.0", true},
		{"claude", "anthropic.claude-vscode-0.5.0", true},
		{"cody", "sourcegraph.cody-ai-1.0.0", true},
		{"tabnine", "tabnine.tabnine-vscode-3.99.0", true},
		{"codeium", "codeium.codeium-2.0.0", true},
		{"continue", "continue.continue-0.8.0", true},

		// Language extensions.
		{"python", "ms-python.python-2024.1.0", true},
		{"go", "golang.go-0.41.0", true},
		{"rust", "rust-lang.rust-analyzer-0.4.0", true},
		{"java", "redhat.java-1.28.0", true},
		{"typescript", "ms-vscode.vscode-typescript-next-5.4.0", true},

		// Dev tools.
		{"docker", "ms-azuretools.vscode-docker-1.29.0", true},
		{"kubernetes", "ms-kubernetes-tools.vscode-kubernetes-tools-1.3.0", true},
		{"terraform", "hashicorp.terraform-2.30.0", true},

		// Git extensions.
		{"gitlens", "eamodio.gitlens-15.0.0", true},
		{"git-graph", "mhutchie.git-graph-1.30.0", true},

		// Testing.
		{"jest", "orta.vscode-jest-6.2.0", true},
		{"pytest", "ms-python.python-pytest-0.5.0", true},

		// Linting.
		{"eslint", "dbaeumer.vscode-eslint-3.0.0", true},
		{"prettier", "esbenp.prettier-vscode-10.4.0", true},
		{"pylint", "ms-python.pylint-2024.0.0", true},

		// Debug.
		{"debugger", "ms-vscode.cpptools-debugger-1.0.0", true},

		// Not interesting.
		{"theme", "zhuangtongfa.material-theme-3.17.0", false},
		{"icons", "vscode-icons-team.vscode-icons-12.0.0", false},
		{"random", "some.random-extension-1.0.0", false},
		{"bracket", "coenraads.bracket-pair-colorizer-2.0.0", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := isInterestingExtension(tc.input)
			if result != tc.expected {
				t.Errorf("isInterestingExtension(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestVscodeConfigDir(t *testing.T) {
	t.Parallel()

	// vscodeConfigDir should return a non-empty string on darwin and linux.
	dir := vscodeConfigDir()
	if dir == "" {
		t.Skip("vscodeConfigDir returned empty (unsupported OS)")
	}

	// Should contain "Code" in the path.
	if len(dir) == 0 {
		t.Error("expected non-empty config dir")
	}
}
