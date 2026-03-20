// Command sigil-plugin-claude is a Claude Code hook that collects AI
// interaction data and pushes it to sigild's plugin ingest endpoint.
//
// Install:
//
//	go install github.com/alecfeeman/sigil-plugin-claude@latest
//
// Then add hooks to ~/.claude/settings.json — see install subcommand.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultIngestURL = "http://127.0.0.1:7775/api/v1/ingest"
	pluginName       = "claude"
)

type PluginEvent struct {
	Plugin      string         `json:"plugin"`
	Kind        string         `json:"kind"`
	Timestamp   time.Time      `json:"timestamp"`
	Correlation map[string]any `json:"correlation,omitempty"`
	Payload     map[string]any `json:"payload"`
}

// Claude Code hook input structure.
type HookInput struct {
	SessionID string    `json:"session_id"`
	ToolName  string    `json:"tool_name"`
	ToolInput any       `json:"tool_input"`
	ToolOutput any      `json:"tool_output,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		installHooks()
		return
	}

	// Running as a hook — read stdin, send event, exit fast.
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) == 0 {
		os.Exit(0) // no input, nothing to do
	}

	var hookInput HookInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		// Not valid JSON — might be raw text, still log it.
		sendEvent("raw_input", map[string]any{"raw": string(input)}, nil)
		os.Exit(0)
	}

	// Determine the hook type from the tool name and context.
	kind := "tool_use"
	if hookInput.ToolOutput != nil {
		kind = "tool_result"
	}
	if hookInput.ToolName == "" {
		kind = "notification"
	}

	// Extract the working directory from Bash tool calls.
	cwd := ""
	if m, ok := hookInput.ToolInput.(map[string]any); ok {
		if c, ok := m["command"].(string); ok {
			_ = c
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	payload := map[string]any{
		"tool_name":  hookInput.ToolName,
		"session_id": hookInput.SessionID,
	}

	// Include tool input summary (not the full content — keep events small).
	if m, ok := hookInput.ToolInput.(map[string]any); ok {
		if cmd, ok := m["command"].(string); ok {
			payload["command"] = truncate(cmd, 200)
		}
		if fp, ok := m["file_path"].(string); ok {
			payload["file_path"] = fp
		}
		if pattern, ok := m["pattern"].(string); ok {
			payload["pattern"] = truncate(pattern, 100)
		}
	}

	if hookInput.Error != "" {
		payload["error"] = truncate(hookInput.Error, 200)
		kind = "tool_error"
	}

	correlation := map[string]any{
		"repo_root": cwd,
	}

	sendEvent(kind, payload, correlation)
}

func sendEvent(kind string, payload, correlation map[string]any) {
	ingestURL := os.Getenv("SIGIL_INGEST_URL")
	if ingestURL == "" {
		ingestURL = defaultIngestURL
	}

	event := PluginEvent{
		Plugin:      pluginName,
		Kind:        kind,
		Timestamp:   time.Now(),
		Correlation: correlation,
		Payload:     payload,
	}

	body, err := json.Marshal(event)
	if err != nil {
		return
	}

	// Fire and forget — don't block the hook.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return // sigild not running, silently skip
	}
	resp.Body.Close()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// installHooks adds sigil-plugin-claude hooks to Claude Code settings.
func installHooks() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", settingsPath, err)
		os.Exit(1)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		fmt.Fprintf(os.Stderr, "cannot parse settings: %v\n", err)
		os.Exit(1)
	}

	// Find the binary path.
	binPath, err := os.Executable()
	if err != nil {
		binPath = "sigil-plugin-claude"
	}

	hookEntry := map[string]any{
		"type":    "command",
		"command": binPath,
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = make(map[string]any)
		settings["hooks"] = hooks
	}

	// Add PostToolUse hook (fires after every tool call with result).
	addHook(hooks, "PostToolUse", hookEntry)

	// Add Notification hook (fires on assistant messages).
	addHook(hooks, "Notification", hookEntry)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot marshal settings: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write %s: %v\n", settingsPath, err)
		os.Exit(1)
	}

	fmt.Println("sigil-plugin-claude: hooks installed in", settingsPath)
	fmt.Println("  PostToolUse → collects tool call results")
	fmt.Println("  Notification → collects assistant messages")
	fmt.Println()
	fmt.Println("Events will be sent to", defaultIngestURL)
	fmt.Println("Set SIGIL_INGEST_URL env var to override.")
}

func addHook(hooks map[string]any, hookName string, entry map[string]any) {
	existing, ok := hooks[hookName].([]any)
	if !ok {
		hooks[hookName] = []any{
			map[string]any{
				"matcher": "",
				"hooks":   []any{entry},
			},
		}
		return
	}

	// Check if already installed.
	for _, item := range existing {
		if m, ok := item.(map[string]any); ok {
			if hookList, ok := m["hooks"].([]any); ok {
				for _, h := range hookList {
					if hm, ok := h.(map[string]any); ok {
						if cmd, ok := hm["command"].(string); ok {
							if filepath.Base(cmd) == "sigil-plugin-claude" {
								fmt.Printf("  %s hook already installed\n", hookName)
								return
							}
						}
					}
				}
			}
		}
	}

	// Append a new catch-all matcher.
	hooks[hookName] = append(existing, map[string]any{
		"matcher": "",
		"hooks":   []any{entry},
	})
}
