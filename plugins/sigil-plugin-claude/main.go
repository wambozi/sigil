// Command sigil-plugin-claude collects AI interaction data from Claude Code
// and can launch Claude Code sessions with context when Sigil recommends a task.
//
// Three modes:
//   - hook:    invoked by Claude Code hooks, captures tool calls → sigild
//   - install: adds hooks to ~/.claude/settings.json
//   - launch:  starts a Claude Code session with a prompt (called by sigild)
//
// Install: ships with sigild (make build / make install).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

type HookInput struct {
	SessionID  string `json:"session_id"`
	ToolName   string `json:"tool_name"`
	ToolInput  any    `json:"tool_input"`
	ToolOutput any    `json:"tool_output,omitempty"`
	Error      string `json:"error,omitempty"`
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			installHooks()
			return
		case "launch":
			launchSession()
			return
		case "capabilities":
			printCapabilities()
			return
		}
	}

	// Default: hook mode — read stdin, send event, exit fast.
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) == 0 {
		os.Exit(0)
	}

	var hookInput HookInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		sendEvent("raw_input", map[string]any{"raw": string(input)}, nil)
		os.Exit(0)
	}

	kind := "tool_use"
	if hookInput.ToolOutput != nil {
		kind = "tool_result"
	}
	if hookInput.ToolName == "" {
		kind = "notification"
	}

	cwd, _ := os.Getwd()

	payload := map[string]any{
		"tool_name":  hookInput.ToolName,
		"session_id": hookInput.SessionID,
	}

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

	sendEvent(kind, payload, map[string]any{"repo_root": cwd})
}

// --- Launch mode: start a Claude Code session with a prompt ---

func launchSession() {
	// Read launch request from args or stdin.
	var prompt, cwd, context string

	fs := flag.NewFlagSet("launch", flag.ExitOnError)
	fs.StringVar(&prompt, "prompt", "", "Prompt to send to Claude Code")
	fs.StringVar(&cwd, "cwd", "", "Working directory for the session")
	fs.StringVar(&context, "context", "", "Additional context to prepend")
	fs.Parse(os.Args[2:])

	// Also accept prompt from stdin if not provided as flag.
	if prompt == "" {
		data, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(data))
	}

	if prompt == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-claude: launch requires --prompt or stdin")
		os.Exit(1)
	}

	// Find claude CLI.
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sigil-plugin-claude: 'claude' not found in PATH")
		os.Exit(1)
	}

	// Build the full prompt with context.
	fullPrompt := prompt
	if context != "" {
		fullPrompt = context + "\n\n" + prompt
	}

	// Launch Claude Code with the prompt.
	cmd := exec.Command(claudeBin, "--print", fullPrompt)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Log the launch event.
	sendEvent("session_launch", map[string]any{
		"prompt_length": len(fullPrompt),
		"cwd":           cwd,
		"mode":          "print",
	}, map[string]any{"repo_root": cwd})

	fmt.Fprintf(os.Stderr, "sigil-plugin-claude: launching Claude Code session\n")

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-claude: claude exited: %v\n", err)
		os.Exit(1)
	}
}

// --- Capabilities: tell sigild what this plugin can do ---

func printCapabilities() {
	caps := map[string]any{
		"plugin": pluginName,
		"actions": []map[string]any{
			{
				"name":        "launch_session",
				"description": "Launch a Claude Code session with a prompt",
				"command":     "sigil-plugin-claude launch --prompt <prompt> [--cwd <dir>] [--context <context>]",
			},
		},
		"data_sources": []string{
			"tool_calls",
			"notifications",
			"errors",
		},
	}
	json.NewEncoder(os.Stdout).Encode(caps)
}

// --- Install hooks ---

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

	addHook(hooks, "PostToolUse", hookEntry)
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
}

func addHook(hooks map[string]any, hookName string, entry map[string]any) {
	existing, ok := hooks[hookName].([]any)
	if !ok {
		hooks[hookName] = []any{
			map[string]any{"matcher": "", "hooks": []any{entry}},
		}
		return
	}
	for _, item := range existing {
		if m, ok := item.(map[string]any); ok {
			if hookList, ok := m["hooks"].([]any); ok {
				for _, h := range hookList {
					if hm, ok := h.(map[string]any); ok {
						if cmd, ok := hm["command"].(string); ok {
							if filepath.Base(cmd) == "sigil-plugin-claude" {
								return // already installed
							}
						}
					}
				}
			}
		}
	}
	hooks[hookName] = append(existing, map[string]any{
		"matcher": "", "hooks": []any{entry},
	})
}

// --- Common ---

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

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
