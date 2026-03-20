//go:build darwin

package sources

import (
	"context"
	"testing"
	"time"
)

func TestFrontApp_returnsNonEmpty(t *testing.T) {
	app := frontApp()
	if app == "" {
		t.Fatal("frontApp() returned empty — lsappinfo may not be available")
	}
	t.Logf("frontmost app: %s", app)
}

func TestDarwinFocusSource_Name(t *testing.T) {
	s := &DarwinFocusSource{}
	if got := s.Name(); got != "focus" {
		t.Errorf("Name() = %q, want %q", got, "focus")
	}
}

func TestDarwinFocusSource_EmitsEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &DarwinFocusSource{Interval: 100 * time.Millisecond}
	ch, err := s.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	// We should get at least one event since the frontmost app is non-empty
	// and lastApp starts as "".
	select {
	case e := <-ch:
		cls, _ := e.Payload["window_class"].(string)
		if cls == "" {
			t.Error("expected non-empty window_class")
		}
		t.Logf("got focus event: %s", cls)
	case <-ctx.Done():
		t.Fatal("timed out waiting for focus event")
	}
}
