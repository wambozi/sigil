package store

import (
	"bytes"
	"context"
	"encoding/json"
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

// --- QueryTopFiles ---------------------------------------------------------

func TestQueryTopFiles(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	insertFile := func(path string, ts time.Time) {
		t.Helper()
		if err := s.InsertEvent(ctx, event.Event{
			Kind: event.KindFile, Source: "files",
			Payload:   map[string]any{"path": path, "op": "WRITE"},
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	insertFile("/a.go", now)
	insertFile("/a.go", now)
	insertFile("/a.go", now)
	insertFile("/b.go", now)
	insertFile("/b.go", now)
	insertFile("/c.go", now)

	// Also insert a non-file event that should be ignored.
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindGit, Source: "git",
		Payload: map[string]any{"path": "/d.go"}, Timestamp: now,
	})

	got, err := s.QueryTopFiles(ctx, now.Add(-time.Second), 2)
	if err != nil {
		t.Fatalf("QueryTopFiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Path != "/a.go" || got[0].Count != 3 {
		t.Errorf("top file: got %+v, want /a.go count=3", got[0])
	}
	if got[1].Path != "/b.go" || got[1].Count != 2 {
		t.Errorf("second file: got %+v, want /b.go count=2", got[1])
	}
}

func TestQueryTopFiles_emptyAndMalformed(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Empty store returns empty slice.
	got, err := s.QueryTopFiles(ctx, time.Now().Add(-time.Hour), 5)
	if err != nil {
		t.Fatalf("QueryTopFiles empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}

	// Insert an event with no "path" field — should be skipped.
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"op": "WRITE"}, Timestamp: time.Now(),
	})

	got, err = s.QueryTopFiles(ctx, time.Now().Add(-time.Hour), 5)
	if err != nil {
		t.Fatalf("QueryTopFiles no-path: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 (no path field), got %d", len(got))
	}
}

// --- QueryTerminalEvents ---------------------------------------------------

func TestQueryTerminalEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	old := now.Add(-2 * time.Hour)

	insert := func(kind event.Kind, ts time.Time) {
		t.Helper()
		_ = s.InsertEvent(ctx, event.Event{
			Kind: kind, Source: "test",
			Payload: map[string]any{"cmd": "go test"}, Timestamp: ts,
		})
	}

	insert(event.KindTerminal, old)
	insert(event.KindTerminal, now)
	insert(event.KindFile, now) // wrong kind

	since := now.Add(-time.Hour)
	got, err := s.QueryTerminalEvents(ctx, since)
	if err != nil {
		t.Fatalf("QueryTerminalEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 terminal event, got %d", len(got))
	}
	if got[0].Kind != event.KindTerminal {
		t.Errorf("Kind: got %q, want %q", got[0].Kind, event.KindTerminal)
	}
	if !got[0].Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v, want %v", got[0].Timestamp, now)
	}
}

// --- QueryHyprlandEvents ---------------------------------------------------

func TestQueryHyprlandEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindHyprland, Source: "hyprland",
		Payload: map[string]any{"window": "firefox"}, Timestamp: now,
	})
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{}, Timestamp: now,
	})

	got, err := s.QueryHyprlandEvents(ctx, now.Add(-time.Second))
	if err != nil {
		t.Fatalf("QueryHyprlandEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 hyprland event, got %d", len(got))
	}
	if got[0].Kind != event.KindHyprland {
		t.Errorf("Kind: got %q, want %q", got[0].Kind, event.KindHyprland)
	}
}

// --- QueryRecentFileEvents -------------------------------------------------

func TestQueryRecentFileEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	old := now.Add(-2 * time.Hour)

	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"path": "/old.go"}, Timestamp: old,
	})
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"path": "/new.go"}, Timestamp: now,
	})
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindGit, Source: "git",
		Payload: map[string]any{}, Timestamp: now,
	})

	since := now.Add(-time.Hour)
	got, err := s.QueryRecentFileEvents(ctx, since)
	if err != nil {
		t.Fatalf("QueryRecentFileEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 recent file event, got %d", len(got))
	}
	if got[0].Payload["path"] != "/new.go" {
		t.Errorf("path: got %v, want /new.go", got[0].Payload["path"])
	}
}

// --- QueryAIInteractions ---------------------------------------------------

func TestQueryAIInteractions(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	old := now.Add(-2 * time.Hour)

	insertAI := func(text, cat, routing string, accepted bool, ts time.Time) {
		t.Helper()
		if err := s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryText: text, QueryCategory: cat,
			Routing: routing, LatencyMS: 100, Accepted: accepted, Timestamp: ts,
		}); err != nil {
			t.Fatalf("InsertAIInteraction: %v", err)
		}
	}

	insertAI("old query", "debug", "local", false, old)
	insertAI("recent query", "code_gen", "cloud", true, now)

	since := now.Add(-time.Hour)
	got, err := s.QueryAIInteractions(ctx, since)
	if err != nil {
		t.Fatalf("QueryAIInteractions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	ai := got[0]
	if ai.QueryText != "recent query" {
		t.Errorf("QueryText: got %q, want %q", ai.QueryText, "recent query")
	}
	if ai.QueryCategory != "code_gen" {
		t.Errorf("QueryCategory: got %q, want %q", ai.QueryCategory, "code_gen")
	}
	if ai.Routing != "cloud" {
		t.Errorf("Routing: got %q, want %q", ai.Routing, "cloud")
	}
	if !ai.Accepted {
		t.Error("Accepted: got false, want true")
	}
	if !ai.Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v, want %v", ai.Timestamp, now)
	}
}

func TestQueryAIInteractions_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryAIInteractions(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryAIInteractions empty: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice from empty table, got len=%d", len(got))
	}
}

// --- QuerySuggestionAcceptanceRate -----------------------------------------

func TestQuerySuggestionAcceptanceRate(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// No resolved suggestions → 0.0.
	rate, err := s.QuerySuggestionAcceptanceRate(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("acceptance rate empty: %v", err)
	}
	if rate != 0.0 {
		t.Errorf("empty rate: got %v, want 0.0", rate)
	}

	// Create 3 suggestions: accept 2, dismiss 1.
	insertAndResolve := func(status SuggestionStatus) {
		t.Helper()
		id, err := s.InsertSuggestion(ctx, Suggestion{
			Category: "pattern", Confidence: 0.8,
			Title: "t", Body: "b", CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("InsertSuggestion: %v", err)
		}
		if err := s.UpdateSuggestionStatus(ctx, id, status); err != nil {
			t.Fatalf("UpdateSuggestionStatus(%s): %v", status, err)
		}
	}

	insertAndResolve(StatusAccepted)
	insertAndResolve(StatusAccepted)
	insertAndResolve(StatusDismissed)

	rate, err = s.QuerySuggestionAcceptanceRate(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("acceptance rate: %v", err)
	}
	// 2 accepted / 3 total ≈ 0.6667
	want := 2.0 / 3.0
	if rate < want-0.01 || rate > want+0.01 {
		t.Errorf("acceptance rate: got %v, want ~%v", rate, want)
	}
}

// --- QueryResolvedSuggestionCount ------------------------------------------

func TestQueryResolvedSuggestionCount(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Empty → 0.
	n, err := s.QueryResolvedSuggestionCount(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("resolved count empty: %v", err)
	}
	if n != 0 {
		t.Errorf("empty count: got %d, want 0", n)
	}

	// Insert: 1 accepted, 1 dismissed, 1 pending (unresolved).
	resolve := func(status SuggestionStatus) {
		t.Helper()
		id, _ := s.InsertSuggestion(ctx, Suggestion{
			Category: "insight", Confidence: 0.5,
			Title: "t", Body: "b", CreatedAt: time.Now(),
		})
		_ = s.UpdateSuggestionStatus(ctx, id, status)
	}

	resolve(StatusAccepted)
	resolve(StatusDismissed)
	resolve(StatusShown) // shown but not resolved

	n, err = s.QueryResolvedSuggestionCount(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("resolved count: %v", err)
	}
	if n != 2 {
		t.Errorf("resolved count: got %d, want 2", n)
	}
}

// --- InsertAction / QueryUndoableActions -----------------------------------

func TestInsertAction_and_QueryUndoableActions(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	// Insert an action that expires in the future.
	err := s.InsertAction(ctx, "act-1", "split build", "make split", "make unsplit",
		now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("InsertAction: %v", err)
	}

	// Insert a duplicate — INSERT OR IGNORE should not error.
	err = s.InsertAction(ctx, "act-1", "dup", "dup", "dup", now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("InsertAction duplicate: %v", err)
	}

	// Insert an already-expired action.
	err = s.InsertAction(ctx, "act-expired", "old action", "cmd", "undo",
		now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("InsertAction expired: %v", err)
	}

	got, err := s.QueryUndoableActions(ctx)
	if err != nil {
		t.Fatalf("QueryUndoableActions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 undoable action, got %d", len(got))
	}
	a := got[0]
	if a.ID != "act-1" {
		t.Errorf("ID: got %q, want %q", a.ID, "act-1")
	}
	if a.Description != "split build" {
		t.Errorf("Description: got %q, want %q", a.Description, "split build")
	}
	if a.ExecuteCmd != "make split" {
		t.Errorf("ExecuteCmd: got %q", a.ExecuteCmd)
	}
	if a.UndoCmd != "make unsplit" {
		t.Errorf("UndoCmd: got %q", a.UndoCmd)
	}
	if a.UndoneAt != nil {
		t.Errorf("UndoneAt: expected nil, got %v", a.UndoneAt)
	}
}

// --- MarkActionUndone ------------------------------------------------------

func TestMarkActionUndone(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	_ = s.InsertAction(ctx, "act-undo", "desc", "exec", "undo", now, now.Add(time.Hour))

	// Before marking undone, it should be undoable.
	got, _ := s.QueryUndoableActions(ctx)
	if len(got) != 1 {
		t.Fatalf("expected 1 undoable before MarkActionUndone, got %d", len(got))
	}

	if err := s.MarkActionUndone(ctx, "act-undo"); err != nil {
		t.Fatalf("MarkActionUndone: %v", err)
	}

	// After marking undone, it should no longer be undoable.
	got, _ = s.QueryUndoableActions(ctx)
	if len(got) != 0 {
		t.Errorf("expected 0 undoable after MarkActionUndone, got %d", len(got))
	}
}

// --- QueryPattern ----------------------------------------------------------

func TestQueryPattern(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	type patternData struct {
		Score int    `json:"score"`
		Name  string `json:"name"`
	}

	// Query non-existent pattern returns sql.ErrNoRows.
	var empty patternData
	err := s.QueryPattern(ctx, "nonexistent", &empty)
	if err == nil {
		t.Fatal("expected error for nonexistent pattern, got nil")
	}

	// Insert and retrieve.
	if err := s.InsertPattern(ctx, "edit_then_test", patternData{Score: 42, Name: "edit-test"}); err != nil {
		t.Fatalf("InsertPattern: %v", err)
	}

	var got patternData
	if err := s.QueryPattern(ctx, "edit_then_test", &got); err != nil {
		t.Fatalf("QueryPattern: %v", err)
	}
	if got.Score != 42 {
		t.Errorf("Score: got %d, want 42", got.Score)
	}
	if got.Name != "edit-test" {
		t.Errorf("Name: got %q, want %q", got.Name, "edit-test")
	}

	// Upsert overwrites.
	if err := s.InsertPattern(ctx, "edit_then_test", patternData{Score: 99, Name: "updated"}); err != nil {
		t.Fatalf("InsertPattern upsert: %v", err)
	}
	var got2 patternData
	if err := s.QueryPattern(ctx, "edit_then_test", &got2); err != nil {
		t.Fatalf("QueryPattern after upsert: %v", err)
	}
	if got2.Score != 99 {
		t.Errorf("Score after upsert: got %d, want 99", got2.Score)
	}
}

// --- Purge -----------------------------------------------------------------

func TestPurge(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Populate some data.
	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"path": "/x.go"}, Timestamp: time.Now(),
	})
	_, _ = s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.8,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	_ = s.InsertPattern(ctx, "test_kind", map[string]any{"key": "val"})
	_ = s.InsertAIInteraction(ctx, event.AIInteraction{
		Routing: "local", LatencyMS: 50, Timestamp: time.Now(),
	})

	// Verify data exists.
	events, _ := s.QueryEvents(ctx, "", 10)
	if len(events) == 0 {
		t.Fatal("expected events before purge")
	}

	if err := s.Purge(); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	// After purge on :memory:, the DB is closed — no further queries possible.
	// The test just verifies Purge completes without error on in-memory DBs.
}

// --- Export ----------------------------------------------------------------

func TestExport(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	_ = s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"path": "/main.go"}, Timestamp: now,
	})
	_, _ = s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.9,
		Title: "Test suggestion", Body: "body text",
		ActionCmd: "go test", CreatedAt: now,
	})

	var buf bytes.Buffer
	if err := s.Export(&buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Should have 2 lines of NDJSON (1 event + 1 suggestion).
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d", len(lines))
	}

	// Verify first line is an event.
	var eventLine map[string]any
	if err := json.Unmarshal(lines[0], &eventLine); err != nil {
		t.Fatalf("unmarshal event line: %v", err)
	}
	if eventLine["type"] != "event" {
		t.Errorf("event type: got %v, want %q", eventLine["type"], "event")
	}
	if eventLine["kind"] != "file" {
		t.Errorf("event kind: got %v, want %q", eventLine["kind"], "file")
	}

	// Verify second line is a suggestion.
	var sgLine map[string]any
	if err := json.Unmarshal(lines[1], &sgLine); err != nil {
		t.Fatalf("unmarshal suggestion line: %v", err)
	}
	if sgLine["type"] != "suggestion" {
		t.Errorf("suggestion type: got %v, want %q", sgLine["type"], "suggestion")
	}
	if sgLine["title"] != "Test suggestion" {
		t.Errorf("suggestion title: got %v", sgLine["title"])
	}
}

func TestExport_empty(t *testing.T) {
	s := openMemory(t)
	var buf bytes.Buffer
	if err := s.Export(&buf); err != nil {
		t.Fatalf("Export empty: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty export, got %d bytes", buf.Len())
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
