package sources

import (
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

func TestParseHyprlandEvent_activeWindow(t *testing.T) {
	e, ok := parseHyprlandEvent("activewindow>>kitty,~/workspace — vim")
	if !ok {
		t.Fatal("expected ok=true for activewindow event")
	}
	if e.Kind != event.KindHyprland {
		t.Fatalf("expected kind=%q, got %q", event.KindHyprland, e.Kind)
	}
	if cls := e.Payload["window_class"]; cls != "kitty" {
		t.Errorf("window_class: got %q, want %q", cls, "kitty")
	}
	if title := e.Payload["window_title"]; title != "~/workspace — vim" {
		t.Errorf("window_title: got %q, want %q", title, "~/workspace — vim")
	}
	if action := e.Payload["action"]; action != "focus" {
		t.Errorf("action: got %q, want %q", action, "focus")
	}
}

func TestParseHyprlandEvent_noComma(t *testing.T) {
	e, ok := parseHyprlandEvent("activewindow>>firefox")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cls := e.Payload["window_class"]; cls != "firefox" {
		t.Errorf("window_class: got %q, want %q", cls, "firefox")
	}
	if title := e.Payload["window_title"]; title != "" {
		t.Errorf("window_title: got %q, want empty", title)
	}
}

func TestParseHyprlandEvent_ignoredEvents(t *testing.T) {
	ignored := []string{
		"workspace>>1",
		"openwindow>>abc,1,kitty,title",
		"closewindow>>abc",
		"",
	}
	for _, line := range ignored {
		if _, ok := parseHyprlandEvent(line); ok {
			t.Errorf("expected ok=false for %q", line)
		}
	}
}

func TestContextSwitchRate(t *testing.T) {
	now := time.Now()
	events := []event.Event{
		{Payload: map[string]any{"window_class": "kitty"}, Timestamp: now},
		{Payload: map[string]any{"window_class": "firefox"}, Timestamp: now.Add(30 * time.Minute)},
		{Payload: map[string]any{"window_class": "kitty"}, Timestamp: now.Add(60 * time.Minute)},
	}
	rate := ContextSwitchRate(events)
	// 3 events over 1 hour = 3.0/hr
	if rate < 2.9 || rate > 3.1 {
		t.Errorf("expected rate ~3.0, got %.2f", rate)
	}
}

func TestContextSwitchRate_tooFewEvents(t *testing.T) {
	events := []event.Event{
		{Payload: map[string]any{"window_class": "kitty"}, Timestamp: time.Now()},
	}
	if rate := ContextSwitchRate(events); rate != 0 {
		t.Errorf("expected 0 for single event, got %.2f", rate)
	}
}

func TestTopWindows(t *testing.T) {
	events := []event.Event{
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": "firefox"}},
		{Payload: map[string]any{"window_class": "firefox"}},
		{Payload: map[string]any{"window_class": "code"}},
	}
	top := TopWindows(events, 2)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].Class != "kitty" {
		t.Errorf("top[0].Class = %q, want kitty", top[0].Class)
	}
	if top[0].Count != 3 {
		t.Errorf("top[0].Count = %d, want 3", top[0].Count)
	}
	if top[1].Class != "firefox" {
		t.Errorf("top[1].Class = %q, want firefox", top[1].Class)
	}
}

func TestGroupFocusByWindow(t *testing.T) {
	events := []event.Event{
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": "firefox"}},
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": ""}},
	}
	counts := GroupFocusByWindow(events)
	if counts["kitty"] != 2 {
		t.Errorf("kitty count = %d, want 2", counts["kitty"])
	}
	if counts["firefox"] != 1 {
		t.Errorf("firefox count = %d, want 1", counts["firefox"])
	}
	if _, ok := counts[""]; ok {
		t.Error("empty class should be excluded")
	}
}

func TestFormatContextSwitchSummary(t *testing.T) {
	events := []event.Event{
		{Payload: map[string]any{"window_class": "kitty"}},
		{Payload: map[string]any{"window_class": "firefox"}},
		{Payload: map[string]any{"window_class": "kitty"}},
	}
	switches, distinct := FormatContextSwitchSummary(events)
	if switches != 3 {
		t.Errorf("switches = %d, want 3", switches)
	}
	if distinct != 2 {
		t.Errorf("distinct = %d, want 2", distinct)
	}
}

func TestWindowFocusEntry_String(t *testing.T) {
	e := WindowFocusEntry{Class: "firefox", Count: 5}
	got := e.String()
	want := "firefox (5)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestHyprlandSource_Name(t *testing.T) {
	s := &HyprlandSource{}
	if got := s.Name(); got != "hyprland" {
		t.Errorf("Name() = %q, want %q", got, "hyprland")
	}
}
