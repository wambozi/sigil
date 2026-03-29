package main

import (
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDefaultSocketPathNonEmpty(t *testing.T) {
	t.Parallel()

	path := defaultSocketPath()
	if path == "" {
		t.Fatal("defaultSocketPath() returned empty string")
	}
}

func TestDefaultSocketPathContainsSigild(t *testing.T) {
	t.Parallel()

	path := defaultSocketPath()
	if !strings.Contains(path, "sigild.sock") {
		t.Errorf("defaultSocketPath() = %q, expected it to contain 'sigild.sock'", path)
	}
}

func TestDefaultSocketPathPlatform(t *testing.T) {
	t.Parallel()

	path := defaultSocketPath()

	switch runtime.GOOS {
	case "darwin":
		// On macOS, should be in TempDir.
		if !strings.HasSuffix(path, "sigild.sock") {
			t.Errorf("darwin: expected path ending with sigild.sock, got %q", path)
		}
	case "linux":
		// On Linux, should be in XDG_RUNTIME_DIR or /run/user/<uid>.
		if !strings.Contains(path, "sigild.sock") {
			t.Errorf("linux: expected path containing sigild.sock, got %q", path)
		}
	case "windows":
		if !strings.Contains(path, "sigil") {
			t.Errorf("windows: expected path containing 'sigil', got %q", path)
		}
	}
}

func TestNewAppReturnsNonNil(t *testing.T) {
	t.Parallel()

	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() returned nil")
	}
}

func TestNewAppHasLogger(t *testing.T) {
	t.Parallel()

	app := NewApp()
	if app.log == nil {
		t.Error("NewApp().log is nil")
	}
}

func TestNewAppDefaultsDisconnected(t *testing.T) {
	t.Parallel()

	app := NewApp()
	if app.connected {
		t.Error("expected new app to be disconnected by default")
	}
}

func TestIsConnectedDefaultFalse(t *testing.T) {
	t.Parallel()

	app := NewApp()
	if app.IsConnected() {
		t.Error("expected IsConnected() to return false for new app")
	}
}

func TestSetConnectedUpdatesState(t *testing.T) {
	t.Parallel()

	app := NewApp()
	// ctx is nil, so Wails EventsEmit is skipped in setConnected.
	app.setConnected(true)
	if !app.IsConnected() {
		t.Error("expected IsConnected() to return true after setConnected(true)")
	}

	app.setConnected(false)
	if app.IsConnected() {
		t.Error("expected IsConnected() to return false after setConnected(false)")
	}
}

func TestSetConnectedIdempotent(t *testing.T) {
	t.Parallel()

	app := NewApp()
	app.setConnected(true)
	app.setConnected(true) // setting same value twice should not panic
	if !app.IsConnected() {
		t.Error("expected still connected")
	}
}

func TestSetConnectedThreadSafety(t *testing.T) {
	t.Parallel()

	app := NewApp()
	var wg sync.WaitGroup

	// Run concurrent setConnected and IsConnected calls to verify thread safety.
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			app.setConnected(true)
		}()
		go func() {
			defer wg.Done()
			_ = app.IsConnected()
		}()
	}
	wg.Wait()

	// After all goroutines, should be connected.
	if !app.IsConnected() {
		t.Error("expected connected after concurrent setConnected(true) calls")
	}
}

func TestHandlePushLineMalformedJSON(t *testing.T) {
	t.Parallel()

	app := NewApp()
	// Should not panic on malformed JSON.
	app.handlePushLine("not json at all")
	app.handlePushLine("")
	app.handlePushLine("{")
	app.handlePushLine("null")
}

func TestHandlePushLineEmptyEvent(t *testing.T) {
	t.Parallel()

	app := NewApp()
	// An event with no "event" field should be ignored (subscription ack).
	app.handlePushLine(`{"ok": true}`)
}

func TestHandlePushLineNonSuggestionEvent(t *testing.T) {
	t.Parallel()

	app := NewApp()
	// An event that's not "suggestions" should not panic (no Wails ctx needed).
	app.handlePushLine(`{"event": "status_update", "payload": {"connected": true}}`)
}

func TestHandlePushLineTableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
	}{
		{"empty object", `{}`},
		{"ok only", `{"ok": true}`},
		{"empty event", `{"event": ""}`},
		{"unknown event", `{"event": "unknown_type"}`},
		{"event with null payload", `{"event": "test", "payload": null}`},
		{"nested object", `{"event": "info", "payload": {"key": "value"}}`},
		{"array payload", `{"event": "test", "payload": [1,2,3]}`},
	}

	app := NewApp()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Should not panic for any of these inputs.
			app.handlePushLine(tc.line)
		})
	}
}
