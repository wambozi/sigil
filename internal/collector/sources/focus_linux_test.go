//go:build linux

package sources

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

func TestDetectBackend_Hyprland(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "abc123")
	t.Setenv("XDG_SESSION_TYPE", "wayland")

	src := NewLinuxFocusSource(testLogger(t))
	if src != nil {
		t.Fatal("expected nil when Hyprland is detected")
	}
}

func TestDetectBackend_SwayWayland(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "sway")
	t.Setenv("SWAYSOCK", "/run/user/1000/sway-ipc.sock")

	backend := detectBackend(testLogger(t))
	if backend == nil {
		t.Fatal("expected sway backend, got nil")
	}
}

func TestDetectBackend_GNOMEWayland(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "GNOME")
	t.Setenv("SWAYSOCK", "")

	backend := detectBackend(testLogger(t))
	if backend == nil {
		t.Fatal("expected GNOME backend, got nil")
	}
}

func TestDetectBackend_KDEWayland(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	t.Setenv("SWAYSOCK", "")

	backend := detectBackend(testLogger(t))
	if backend == nil {
		t.Fatal("expected KDE backend, got nil")
	}
}

func TestDetectBackend_NoCompositor(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "unknown")
	t.Setenv("SWAYSOCK", "")

	backend := detectBackend(testLogger(t))
	if backend != nil {
		t.Fatal("expected nil for unsupported compositor")
	}
}

func TestDetectBackend_SwaySocketFallback(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("XDG_SESSION_TYPE", "") // not set
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("SWAYSOCK", "/run/user/1000/sway-ipc.sock")

	backend := detectBackend(testLogger(t))
	if backend == nil {
		t.Fatal("expected sway backend via SWAYSOCK fallback, got nil")
	}
}

func TestSearchFocused_Simple(t *testing.T) {
	tree := map[string]any{
		"focused": true,
		"app_id":  "firefox",
		"name":    "GitHub - Mozilla Firefox",
		"nodes":   []any{},
	}
	data, _ := json.Marshal(tree)
	class, title := searchFocused(data)
	if class != "firefox" {
		t.Errorf("class = %q, want %q", class, "firefox")
	}
	if title != "GitHub - Mozilla Firefox" {
		t.Errorf("title = %q, want %q", title, "GitHub - Mozilla Firefox")
	}
}

func TestSearchFocused_Nested(t *testing.T) {
	tree := map[string]any{
		"focused": false,
		"app_id":  "",
		"name":    "root",
		"nodes": []any{
			map[string]any{
				"focused": false,
				"app_id":  "",
				"name":    "workspace",
				"nodes": []any{
					map[string]any{
						"focused":        true,
						"app_id":         "kitty",
						"name":           "~/code - vim",
						"nodes":          []any{},
						"floating_nodes": []any{},
					},
				},
				"floating_nodes": []any{},
			},
		},
		"floating_nodes": []any{},
	}
	data, _ := json.Marshal(tree)
	class, title := searchFocused(data)
	if class != "kitty" {
		t.Errorf("class = %q, want %q", class, "kitty")
	}
	if title != "~/code - vim" {
		t.Errorf("title = %q, want %q", title, "~/code - vim")
	}
}

func TestSearchFocused_FloatingNode(t *testing.T) {
	tree := map[string]any{
		"focused": false,
		"app_id":  "",
		"name":    "root",
		"nodes":   []any{},
		"floating_nodes": []any{
			map[string]any{
				"focused":        true,
				"app_id":         "pavucontrol",
				"name":           "Volume Control",
				"nodes":          []any{},
				"floating_nodes": []any{},
			},
		},
	}
	data, _ := json.Marshal(tree)
	class, title := searchFocused(data)
	if class != "pavucontrol" {
		t.Errorf("class = %q, want %q", class, "pavucontrol")
	}
	if title != "Volume Control" {
		t.Errorf("title = %q, want %q", title, "Volume Control")
	}
}

func TestSearchFocused_NoFocused(t *testing.T) {
	tree := map[string]any{
		"focused": false,
		"app_id":  "",
		"name":    "root",
		"nodes":   []any{},
	}
	data, _ := json.Marshal(tree)
	class, title := searchFocused(data)
	if class != "" || title != "" {
		t.Errorf("expected empty, got class=%q title=%q", class, title)
	}
}

func TestLinuxFocusSourceName(t *testing.T) {
	src := &LinuxFocusSource{}
	if got := src.Name(); got != "linux-focus" {
		t.Errorf("Name() = %q, want %q", got, "linux-focus")
	}
}

func TestPollDedup(t *testing.T) {
	calls := 0
	mockBackend := func(_ context.Context) (string, string, error) {
		calls++
		return "firefox", "GitHub", nil
	}

	src := &LinuxFocusSource{
		log:      testLogger(t),
		interval: time.Millisecond,
		backend:  mockBackend,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ch, err := src.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var events []event.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Backend was called many times, but only one event should be emitted
	// because the window never changed.
	if len(events) != 1 {
		t.Errorf("expected 1 event (dedup), got %d", len(events))
	}
	if calls < 2 {
		t.Errorf("expected backend called multiple times, got %d", calls)
	}

	// Verify event content.
	if len(events) > 0 {
		ev := events[0]
		if ev.Source != "linux-focus" {
			t.Errorf("Source = %q, want %q", ev.Source, "linux-focus")
		}
		if ev.Kind != event.KindHyprland {
			t.Errorf("Kind = %q, want %q", ev.Kind, event.KindHyprland)
		}
		if cls := ev.Payload["window_class"]; cls != "firefox" {
			t.Errorf("window_class = %q, want %q", cls, "firefox")
		}
	}
}

func TestPollEmitsOnChange(t *testing.T) {
	callCount := 0
	mockBackend := func(_ context.Context) (string, string, error) {
		callCount++
		if callCount <= 3 {
			return "firefox", "GitHub", nil
		}
		return "kitty", "~/code - vim", nil
	}

	src := &LinuxFocusSource{
		log:      testLogger(t),
		interval: time.Millisecond,
		backend:  mockBackend,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ch, err := src.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var events []event.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should get exactly 2 events: one for firefox, one for kitty.
	if len(events) != 2 {
		t.Errorf("expected 2 events (change detected), got %d", len(events))
	}
}

// testLogger returns a no-op logger for tests.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.Default()
}
