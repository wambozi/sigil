package analyzer

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/store"
)

func openMemoryStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- buildPrompt -----------------------------------------------------------

func TestBuildPrompt_containsEventCounts(t *testing.T) {
	s := &Summary{
		Period: time.Hour,
		EventCounts: map[event.Kind]int64{
			event.KindFile:    12,
			event.KindProcess: 5,
			event.KindGit:     3,
			event.KindAI:      1,
		},
	}

	prompt := buildPrompt(s)

	checks := []string{"1h0m0s", "12", "5", "3", "1"}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_zeroCounts(t *testing.T) {
	s := &Summary{
		Period:      30 * time.Minute,
		EventCounts: map[event.Kind]int64{},
	}
	prompt := buildPrompt(s)
	if prompt == "" {
		t.Error("buildPrompt returned empty string for zero-count summary")
	}
}

// --- localPass -------------------------------------------------------------

func TestLocalPass_countsMatchInserted(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	interval := time.Hour
	now := time.Now()

	insert := func(k event.Kind, ts time.Time) {
		t.Helper()
		err := db.InsertEvent(ctx, event.Event{
			Kind: k, Source: "test",
			Payload:   map[string]any{},
			Timestamp: ts,
		})
		if err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	// Within the analysis window.
	insert(event.KindFile, now.Add(-30*time.Minute))
	insert(event.KindFile, now.Add(-45*time.Minute))
	insert(event.KindGit, now.Add(-10*time.Minute))
	// Outside the window — should not be counted.
	insert(event.KindFile, now.Add(-2*time.Hour))

	a := New(db, nil, interval, newTestLogger())
	summary, err := a.localPass(ctx)
	if err != nil {
		t.Fatalf("localPass: %v", err)
	}

	if summary.EventCounts[event.KindFile] != 2 {
		t.Errorf("file events: got %d, want 2", summary.EventCounts[event.KindFile])
	}
	if summary.EventCounts[event.KindGit] != 1 {
		t.Errorf("git events: got %d, want 1", summary.EventCounts[event.KindGit])
	}
	if summary.Period != interval {
		t.Errorf("Period: got %v, want %v", summary.Period, interval)
	}
}

func TestLocalPass_emptyStore(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	a := New(db, nil, time.Hour, newTestLogger())
	summary, err := a.localPass(ctx)
	if err != nil {
		t.Fatalf("localPass on empty store: %v", err)
	}
	for k, n := range summary.EventCounts {
		if n != 0 {
			t.Errorf("expected 0 for %s, got %d", k, n)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
