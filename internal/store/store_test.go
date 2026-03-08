package store

import (
	"context"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

func openMemory(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- InsertEvent / QueryEvents ---------------------------------------------

func TestInsertAndQueryEvents_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond) // SQLite stores ms precision
	e := event.Event{
		Kind:      event.KindFile,
		Source:    "files",
		Payload:   map[string]any{"path": "/home/nick/code/main.go", "op": "WRITE"},
		Timestamp: now,
	}

	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	got, err := s.QueryEvents(ctx, "", 10)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}

	g := got[0]
	if g.Kind != event.KindFile {
		t.Errorf("Kind: got %q, want %q", g.Kind, event.KindFile)
	}
	if g.Source != "files" {
		t.Errorf("Source: got %q, want %q", g.Source, "files")
	}
	if g.Payload["path"] != "/home/nick/code/main.go" {
		t.Errorf("Payload[path]: got %v", g.Payload["path"])
	}
	if !g.Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v, want %v", g.Timestamp, now)
	}
}

func TestQueryEvents_filterByKind(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	insert := func(k event.Kind) {
		t.Helper()
		err := s.InsertEvent(ctx, event.Event{
			Kind:      k,
			Source:    "test",
			Payload:   map[string]any{},
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("InsertEvent(%s): %v", k, err)
		}
	}

	insert(event.KindFile)
	insert(event.KindGit)
	insert(event.KindFile)

	files, err := s.QueryEvents(ctx, event.KindFile, 10)
	if err != nil {
		t.Fatalf("QueryEvents(file): %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 file events, got %d", len(files))
	}

	gits, err := s.QueryEvents(ctx, event.KindGit, 10)
	if err != nil {
		t.Fatalf("QueryEvents(git): %v", err)
	}
	if len(gits) != 1 {
		t.Errorf("expected 1 git event, got %d", len(gits))
	}
}

func TestQueryEvents_emptyStore(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	got, err := s.QueryEvents(ctx, "", 10)
	if err != nil {
		t.Fatalf("QueryEvents on empty store: %v", err)
	}
	// Must return an empty slice, not nil (callers range over the result).
	if got == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events, got %d", len(got))
	}
}

func TestQueryEvents_limit(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = s.InsertEvent(ctx, event.Event{
			Kind:      event.KindProcess,
			Source:    "proc",
			Payload:   map[string]any{},
			Timestamp: time.Now(),
		})
	}

	got, err := s.QueryEvents(ctx, "", 3)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 events (limit), got %d", len(got))
	}
}

// --- CountEvents -----------------------------------------------------------

func TestCountEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	past := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-30 * time.Minute)

	insert := func(k event.Kind, ts time.Time) {
		t.Helper()
		if err := s.InsertEvent(ctx, event.Event{
			Kind: k, Source: "test",
			Payload:   map[string]any{},
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	insert(event.KindFile, past)   // older than 1h window
	insert(event.KindFile, recent) // within 1h window
	insert(event.KindGit, recent)  // different kind

	since := time.Now().Add(-time.Hour)

	n, err := s.CountEvents(ctx, event.KindFile, since)
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if n != 1 {
		t.Errorf("CountEvents(file, last 1h): got %d, want 1", n)
	}
}

// --- InsertAIInteraction ---------------------------------------------------

func TestInsertAIInteraction(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	ai := event.AIInteraction{
		QueryText:     "summarise my activity",
		QueryCategory: "workflow_analysis",
		Routing:       "local",
		LatencyMS:     142,
		Accepted:      true,
		Timestamp:     time.Now(),
	}
	if err := s.InsertAIInteraction(ctx, ai); err != nil {
		t.Fatalf("InsertAIInteraction: %v", err)
	}
}

// --- InsertPattern ---------------------------------------------------------

func TestInsertPattern_upsert(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	type payload struct{ Score int }

	if err := s.InsertPattern(ctx, "edit_then_test", payload{Score: 3}); err != nil {
		t.Fatalf("first InsertPattern: %v", err)
	}
	// Second insert with same kind should UPDATE, not fail.
	if err := s.InsertPattern(ctx, "edit_then_test", payload{Score: 7}); err != nil {
		t.Fatalf("upsert InsertPattern: %v", err)
	}
}

// --- InsertSuggestion / QuerySuggestions -----------------------------------

func TestInsertAndQuerySuggestions_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	sg := Suggestion{
		Category:   "pattern",
		Confidence: 0.75,
		Title:      "Run tests after editing collector",
		Body:       "You edited files in /internal/collector and usually run tests next.",
		ActionCmd:  "go test ./internal/collector/...",
		CreatedAt:  time.Now().Truncate(time.Millisecond),
	}

	id, err := s.InsertSuggestion(ctx, sg)
	if err != nil {
		t.Fatalf("InsertSuggestion: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	got, err := s.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("QuerySuggestions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(got))
	}

	g := got[0]
	if g.Category != "pattern" {
		t.Errorf("Category: got %q, want %q", g.Category, "pattern")
	}
	if g.Confidence != 0.75 {
		t.Errorf("Confidence: got %v, want 0.75", g.Confidence)
	}
	if g.Title != sg.Title {
		t.Errorf("Title: got %q, want %q", g.Title, sg.Title)
	}
	if g.ActionCmd != sg.ActionCmd {
		t.Errorf("ActionCmd: got %q, want %q", g.ActionCmd, sg.ActionCmd)
	}
	if g.Status != StatusPending {
		t.Errorf("Status: got %q, want %q", g.Status, StatusPending)
	}
}

func TestQuerySuggestions_filterByStatus(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	insert := func(title string) int64 {
		t.Helper()
		id, err := s.InsertSuggestion(ctx, Suggestion{
			Category: "insight", Confidence: 0.6,
			Title: title, Body: "body", CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("InsertSuggestion: %v", err)
		}
		return id
	}

	id1 := insert("first")
	insert("second")

	// Mark one as shown.
	if err := s.UpdateSuggestionStatus(ctx, id1, StatusShown); err != nil {
		t.Fatalf("UpdateSuggestionStatus: %v", err)
	}

	shown, err := s.QuerySuggestions(ctx, StatusShown, 10)
	if err != nil {
		t.Fatalf("QuerySuggestions(shown): %v", err)
	}
	if len(shown) != 1 {
		t.Errorf("expected 1 shown suggestion, got %d", len(shown))
	}

	pending, err := s.QuerySuggestions(ctx, StatusPending, 10)
	if err != nil {
		t.Fatalf("QuerySuggestions(pending): %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending suggestion, got %d", len(pending))
	}
}

func TestUpdateSuggestionStatus_setsTimestamps(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "reminder", Confidence: 0.7,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertSuggestion: %v", err)
	}

	if err := s.UpdateSuggestionStatus(ctx, id, StatusShown); err != nil {
		t.Fatalf("UpdateSuggestionStatus(shown): %v", err)
	}
	got, _ := s.QuerySuggestions(ctx, StatusShown, 1)
	if got[0].ShownAt == nil {
		t.Error("expected shown_at to be set after StatusShown")
	}

	if err := s.UpdateSuggestionStatus(ctx, id, StatusAccepted); err != nil {
		t.Fatalf("UpdateSuggestionStatus(accepted): %v", err)
	}
	got, _ = s.QuerySuggestions(ctx, StatusAccepted, 1)
	if got[0].ResolvedAt == nil {
		t.Error("expected resolved_at to be set after StatusAccepted")
	}
}

func TestQuerySuggestions_emptyStore(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	got, err := s.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("QuerySuggestions on empty store: %v", err)
	}
	if got == nil {
		t.Error("expected empty slice, got nil")
	}
}

// --- InsertFeedback --------------------------------------------------------

func TestInsertFeedback(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.8,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertSuggestion: %v", err)
	}

	if err := s.InsertFeedback(ctx, id, "accepted"); err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
}

// --- migrate idempotency ---------------------------------------------------

func TestMigrate_idempotent(t *testing.T) {
	s := openMemory(t)
	// Running migrate again on an already-migrated DB must not error.
	if err := migrate(s.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
