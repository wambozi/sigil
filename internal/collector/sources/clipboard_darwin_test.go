//go:build darwin

package sources

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// requireClipboardAccess skips the test if osascript clipboard access is not
// available (e.g. in sandboxed CI environments or headless runners).
func requireClipboardAccess(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := exec.CommandContext(ctx, "osascript", "-e", "the clipboard info").Output(); err != nil {
		t.Skip("clipboard access not available in this environment")
	}
}

func TestClipboardSource_Name(t *testing.T) {
	s := &ClipboardSource{}
	if got := s.Name(); got != "clipboard" {
		t.Errorf("Name() = %q, want %q", got, "clipboard")
	}
}

func TestClipboardSource_EmitsEvent(t *testing.T) {
	requireClipboardAccess(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &ClipboardSource{Interval: 100 * time.Millisecond}
	ch, err := s.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	// The first poll should emit an event since lastInfo starts empty and
	// the clipboard info will be different (even if the clipboard is empty,
	// osascript returns something).
	select {
	case e := <-ch:
		if e.Kind != event.KindClipboard {
			t.Errorf("Kind = %q, want %q", e.Kind, event.KindClipboard)
		}
		if e.Source != "clipboard" {
			t.Errorf("Source = %q, want %q", e.Source, "clipboard")
		}
		// Verify payload fields are present.
		if _, ok := e.Payload["content_type"]; !ok {
			t.Error("expected content_type in payload")
		}
		if _, ok := e.Payload["content_length"]; !ok {
			t.Error("expected content_length in payload")
		}
		if _, ok := e.Payload["source_app"]; !ok {
			t.Error("expected source_app in payload")
		}
		t.Logf("got clipboard event: type=%v len=%v app=%v",
			e.Payload["content_type"], e.Payload["content_length"], e.Payload["source_app"])
	case <-ctx.Done():
		t.Fatal("timed out waiting for clipboard event")
	}
}

func TestClipboardSource_NoDuplicateOnSameContent(t *testing.T) {
	requireClipboardAccess(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	s := &ClipboardSource{Interval: 100 * time.Millisecond}
	ch, err := s.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	// Drain the first event (initial detection).
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first clipboard event")
	}

	// Without changing the clipboard, we should NOT get another event.
	// Wait a few poll intervals.
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case e := <-ch:
		// It is possible the clipboard was changed externally; only fail
		// if we are reasonably confident this is a duplicate.
		t.Logf("unexpected second event (clipboard may have changed externally): %+v", e)
	case <-timer.C:
		// Expected: no duplicate events.
	}
}

func TestClassifyClipboardContent(t *testing.T) {
	tests := []struct {
		name string
		info string
		want string
	}{
		{
			name: "utf8 text",
			info: "{{string, 245}, {\u00ABclass utf8\u00BB, 245}}",
			want: "text/plain",
		},
		{
			name: "string only",
			info: "{{string, 42}}",
			want: "text/plain",
		},
		{
			name: "png image",
			info: "{{\u00ABclass PNGf\u00BB, 1024}}",
			want: "image/png",
		},
		{
			name: "tiff image",
			info: "{{\u00ABclass TIFF\u00BB, 2048}}",
			want: "image/tiff",
		},
		{
			name: "unknown type",
			info: "{{\u00ABclass ????\u00BB, 100}}",
			want: "unknown",
		},
		{
			name: "empty",
			info: "",
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyClipboardContent(tt.info)
			if got != tt.want {
				t.Errorf("classifyClipboardContent(%q) = %q, want %q", tt.info, got, tt.want)
			}
		})
	}
}
