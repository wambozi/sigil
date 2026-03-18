// Command sigil-plugin-claude collects AI interaction data from Claude Code
// sessions and pushes events to sigild's plugin ingest endpoint.
//
// It works by registering as a Claude Code hook that fires on conversation
// turns, tool calls, and session start/end. Each event is normalized and
// POSTed to sigild.
//
// Usage:
//
//	sigil-plugin-claude --sigil-ingest-url http://127.0.0.1:7775/api/v1/ingest
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Event struct {
	Plugin      string         `json:"plugin"`
	Kind        string         `json:"kind"`
	Timestamp   time.Time      `json:"timestamp"`
	Correlation map[string]any `json:"correlation,omitempty"`
	Payload     map[string]any `json:"payload"`
}

var ingestURL string

func main() {
	flag.StringVar(&ingestURL, "sigil-ingest-url", "http://127.0.0.1:7775/api/v1/ingest", "Sigil ingest URL")
	flag.Parse()

	// The claude plugin runs in two modes:
	// 1. Hook mode: invoked by Claude Code as a hook, processes stdin, sends event, exits
	// 2. Daemon mode: watches for Claude Code sessions and emits events

	// For now, implement hook mode — Claude Code invokes this binary with
	// event data on stdin.
	if isHookInvocation() {
		handleHook()
		return
	}

	// Daemon mode: watch for Claude Code processes and session files.
	fmt.Fprintln(os.Stderr, "sigil-plugin-claude: running in daemon mode")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
}

// isHookInvocation returns true if we're being called as a Claude Code hook
// (stdin has data or CLAUDE_HOOK env var is set).
func isHookInvocation() bool {
	return os.Getenv("CLAUDE_HOOK_EVENT") != ""
}

// handleHook processes a Claude Code hook invocation.
func handleHook() {
	hookEvent := os.Getenv("CLAUDE_HOOK_EVENT") // e.g. "assistant_turn", "tool_call"
	cwd := os.Getenv("CLAUDE_CWD")
	model := os.Getenv("CLAUDE_MODEL")
	sessionID := os.Getenv("CLAUDE_SESSION_ID")

	event := Event{
		Plugin:    "claude",
		Kind:      "hook_" + hookEvent,
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": cwd,
		},
		Payload: map[string]any{
			"hook_event": hookEvent,
			"model":      model,
			"session_id": sessionID,
			"cwd":        cwd,
		},
	}

	// Read stdin for additional hook data (if any).
	var hookData map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&hookData); err == nil {
		for k, v := range hookData {
			event.Payload[k] = v
		}
	}

	send([]Event{event})
}

func send(events []Event) {
	body, err := json.Marshal(events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-claude: marshal error: %v\n", err)
		return
	}

	resp, err := http.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-claude: send error: %v\n", err)
		return
	}
	resp.Body.Close()
}
