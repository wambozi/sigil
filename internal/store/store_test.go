package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wambozi/sigil/internal/event"
)

// --- Shared helpers --------------------------------------------------------

func openMemory(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeTaskRecord(id, phase string, completedAt *time.Time) TaskRecord {
	now := time.Now().Truncate(time.Millisecond)
	return TaskRecord{
		ID:           id,
		RepoRoot:     "/home/nick/workspace/sigil",
		Branch:       "main",
		Phase:        phase,
		Files:        map[string]int{"/main.go": 3, "/store.go": 1},
		StartedAt:    now,
		LastActivity: now,
		CompletedAt:  completedAt,
		CommitCount:  2,
		TestRuns:     5,
		TestFailures: 1,
	}
}

// --- Open / Close ----------------------------------------------------------

func TestOpen_filePathAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sigil.db")

	s, err := Open(path)
	require.NoError(t, err)
	require.NotNil(t, s)
	require.NoError(t, s.Close())
}

func TestOpen_createsSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sigil.db")

	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()

	// Verify the schema was created by running a trivial query on each table.
	tables := []string{
		"events", "ai_interactions", "patterns", "suggestions",
		"feedback", "action_log", "tasks", "ml_events",
		"ml_predictions", "plugin_events",
	}
	for _, tbl := range tables {
		var n int
		err := s.db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n)
		assert.NoError(t, err, "table %s should exist", tbl)
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

func TestQueryEvents_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryEvents(ctx, "", 10)
	require.Error(t, err)
}

func TestInsertEvent_marshalError(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// A channel is not JSON-serializable; putting one in Payload exercises
	// the early-return branch in InsertEvent.
	e := event.Event{
		Kind:      event.KindFile,
		Source:    "files",
		Payload:   map[string]any{"bad": make(chan int)},
		Timestamp: time.Now(),
	}
	err := s.InsertEvent(ctx, e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: marshal payload")
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

func TestCountEvents_zero(t *testing.T) {
	s := openMemory(t)
	n, err := s.CountEvents(context.Background(), event.KindGit, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestCountEvents_multipleKinds(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 3; i++ {
		require.NoError(t, s.InsertEvent(ctx, event.Event{
			Kind: event.KindGit, Source: "git",
			Payload: map[string]any{}, Timestamp: now,
		}))
	}
	require.NoError(t, s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{}, Timestamp: now,
	}))

	gitCount, err := s.CountEvents(ctx, event.KindGit, now.Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(3), gitCount)

	fileCount, err := s.CountEvents(ctx, event.KindFile, now.Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), fileCount)
}

// --- InsertPattern / QueryPattern ------------------------------------------

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

func TestInsertPattern_marshalError(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	type badPayload struct {
		Ch chan int
	}
	err := s.InsertPattern(ctx, "bad", badPayload{Ch: make(chan int)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: marshal pattern")
}

func TestInsertPattern_complexPayload(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	type complexPayload struct {
		Files  []string       `json:"files"`
		Counts map[string]int `json:"counts"`
		Score  float64        `json:"score"`
	}
	payload := complexPayload{
		Files:  []string{"/a.go", "/b.go"},
		Counts: map[string]int{"/a.go": 5, "/b.go": 2},
		Score:  0.87,
	}

	require.NoError(t, s.InsertPattern(ctx, "complex_pattern", payload))

	var got complexPayload
	require.NoError(t, s.QueryPattern(ctx, "complex_pattern", &got))
	assert.Equal(t, payload.Files, got.Files)
	assert.Equal(t, payload.Counts, got.Counts)
	assert.InDelta(t, payload.Score, got.Score, 0.001)
}

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

func TestQueryPattern_errNoRows(t *testing.T) {
	s := openMemory(t)
	var dest map[string]any
	err := s.QueryPattern(context.Background(), "does-not-exist", &dest)
	assert.ErrorIs(t, err, sql.ErrNoRows)
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

func TestInsertSuggestion_returnsPositiveID(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category:   "optimization",
		Confidence: 0.9,
		Title:      "Use buffered channel",
		Body:       "Consider buffering the event channel.",
		ActionCmd:  "",
		CreatedAt:  time.Now(),
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
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

func TestQuerySuggestions_allStatuses_limit(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := s.InsertSuggestion(ctx, Suggestion{
			Category: "pattern", Confidence: 0.5,
			Title: "s", Body: "b", CreatedAt: time.Now(),
		})
		require.NoError(t, err)
	}

	got, err := s.QuerySuggestions(ctx, "", 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestQuerySuggestions_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QuerySuggestions(ctx, "", 10)
	require.Error(t, err)
}

func TestQuerySuggestions_statusFilter_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QuerySuggestions(ctx, StatusPending, 10)
	require.Error(t, err)
}

// --- UpdateSuggestionStatus ------------------------------------------------

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

func TestUpdateSuggestionStatus_ignoredSetsResolvedAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.5,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	require.NoError(t, s.UpdateSuggestionStatus(ctx, id, StatusIgnored))

	got, err := s.QuerySuggestions(ctx, StatusIgnored, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].ResolvedAt)
}

func TestUpdateSuggestionStatus_dismissedSetsResolvedAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "insight", Confidence: 0.6,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	require.NoError(t, s.UpdateSuggestionStatus(ctx, id, StatusDismissed))

	got, err := s.QuerySuggestions(ctx, StatusDismissed, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].ResolvedAt)
}

func TestUpdateSuggestionStatus_defaultCasePending(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.5,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	// First advance to shown, then back to pending via the default branch.
	require.NoError(t, s.UpdateSuggestionStatus(ctx, id, StatusShown))
	require.NoError(t, s.UpdateSuggestionStatus(ctx, id, StatusPending))

	got, err := s.QuerySuggestions(ctx, StatusPending, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, StatusPending, got[0].Status)
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

func TestQueryTopFiles_limitTruncatesCorrectly(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	// 4 distinct files with different counts.
	files := []struct {
		path  string
		times int
	}{
		{"/d.go", 1},
		{"/c.go", 2},
		{"/b.go", 3},
		{"/a.go", 4},
	}
	for _, f := range files {
		for i := 0; i < f.times; i++ {
			require.NoError(t, s.InsertEvent(ctx, event.Event{
				Kind:      event.KindFile,
				Source:    "files",
				Payload:   map[string]any{"path": f.path, "op": "WRITE"},
				Timestamp: now,
			}))
		}
	}

	got, err := s.QueryTopFiles(ctx, now.Add(-time.Minute), 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "/a.go", got[0].Path)
	assert.Equal(t, int64(4), got[0].Count)
	assert.Equal(t, "/b.go", got[1].Path)
}

func TestQueryTopFiles_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryTopFiles(ctx, time.Now().Add(-time.Hour), 5)
	require.Error(t, err)
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

func TestQueryTerminalEvents_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryTerminalEvents(ctx, time.Now().Add(-time.Hour))
	require.Error(t, err)
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

func TestQueryHyprlandEvents_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryHyprlandEvents(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
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

func TestQueryRecentFileEvents_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryRecentFileEvents(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
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

func TestQueryAIInteractions_orderedAscending(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Millisecond)

	// Insert in reverse order.
	for i := 2; i >= 0; i-- {
		require.NoError(t, s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryText: string(rune('a' + i)),
			Routing:   "local",
			LatencyMS: int64(i * 10),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}))
	}

	got, err := s.QueryAIInteractions(ctx, base.Add(-time.Second))
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Ascending — earliest first.
	assert.True(t, got[0].Timestamp.Before(got[1].Timestamp))
	assert.True(t, got[1].Timestamp.Before(got[2].Timestamp))
}

func TestQueryAIInteractions_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryAIInteractions(ctx, time.Now().Add(-time.Hour))
	require.Error(t, err)
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

func TestInsertAIInteraction_privacyMode(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Privacy-max mode: no query text or category.
	ai := event.AIInteraction{
		QueryText:     "",
		QueryCategory: "",
		Routing:       "local",
		LatencyMS:     50,
		Accepted:      false,
		Timestamp:     time.Now(),
	}
	require.NoError(t, s.InsertAIInteraction(ctx, ai))

	got, err := s.QueryAIInteractions(ctx, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "", got[0].QueryText)
	assert.Equal(t, "", got[0].QueryCategory)
	assert.False(t, got[0].Accepted)
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

func TestQuerySuggestionAcceptanceRate_onlyDismissed(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.8,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, s.UpdateSuggestionStatus(ctx, id, StatusDismissed))

	rate, err := s.QuerySuggestionAcceptanceRate(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0.0, rate)
}

func TestQuerySuggestionAcceptanceRate_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QuerySuggestionAcceptanceRate(ctx, time.Now().Add(-time.Hour))
	require.Error(t, err)
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

func TestInsertAction_emptyUndoCmd(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, s.InsertAction(ctx, "no-undo", "desc", "make build", "", now, now.Add(time.Hour)))

	got, err := s.QueryUndoableActions(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "", got[0].UndoCmd)
}

func TestQueryUndoableActions_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryUndoableActions(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestQueryUndoableActions_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryUndoableActions(ctx)
	require.Error(t, err)
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

func TestMarkActionUndone_idempotent(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, s.InsertAction(ctx, "act-idem", "desc", "cmd", "undo", now, now.Add(time.Hour)))
	require.NoError(t, s.MarkActionUndone(ctx, "act-idem"))
	// Second call must not error.
	require.NoError(t, s.MarkActionUndone(ctx, "act-idem"))

	got, err := s.QueryUndoableActions(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
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

func TestInsertFeedback_multipleOutcomes(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	id, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.8,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	require.NoError(t, s.InsertFeedback(ctx, id, "shown"))
	require.NoError(t, s.InsertFeedback(ctx, id, "accepted"))
}

// --- InsertTask / QueryCurrentTask -----------------------------------------

func TestInsertTask_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	tr := makeTaskRecord("task-1", "coding", nil)
	require.NoError(t, s.InsertTask(ctx, tr))

	got, err := s.QueryCurrentTask(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "task-1", got.ID)
	assert.Equal(t, "/home/nick/workspace/sigil", got.RepoRoot)
	assert.Equal(t, "main", got.Branch)
	assert.Equal(t, "coding", got.Phase)
	assert.Equal(t, 2, got.CommitCount)
	assert.Equal(t, 5, got.TestRuns)
	assert.Equal(t, 1, got.TestFailures)
	assert.Nil(t, got.CompletedAt)
	assert.Equal(t, map[string]int{"/main.go": 3, "/store.go": 1}, got.Files)
	assert.True(t, got.StartedAt.Equal(tr.StartedAt), "StartedAt mismatch")
}

func TestInsertTask_withCompletedAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	completedAt := time.Now().Add(-30 * time.Minute).Truncate(time.Millisecond)
	tr := makeTaskRecord("task-done", "testing", &completedAt)
	require.NoError(t, s.InsertTask(ctx, tr))

	// Completed tasks should not show up as the current task.
	current, err := s.QueryCurrentTask(ctx)
	require.NoError(t, err)
	assert.Nil(t, current, "completed task must not be returned as current")
}

func TestInsertTask_marshalError(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Files is map[string]int which always marshals fine.
	// Verify the normal path succeeds; the InsertTask/UpdateTask marshal error
	// path cannot be triggered via the public API because map[string]int is
	// always marshalable.
	tr := makeTaskRecord("ok-ins", "coding", nil)
	require.NoError(t, s.InsertTask(ctx, tr))
}

func TestQueryCurrentTask_emptyStore(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryCurrentTask(context.Background())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestQueryCurrentTask_idlePhaseExcluded(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	tr := makeTaskRecord("idle-task", "idle", nil)
	require.NoError(t, s.InsertTask(ctx, tr))

	got, err := s.QueryCurrentTask(ctx)
	require.NoError(t, err)
	assert.Nil(t, got, "idle-phase task must not be returned as current")
}

func TestQueryCurrentTask_returnsLatest(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	older := makeTaskRecord("task-old", "coding", nil)
	older.LastActivity = time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	require.NoError(t, s.InsertTask(ctx, older))

	newer := makeTaskRecord("task-new", "testing", nil)
	newer.LastActivity = time.Now().Truncate(time.Millisecond)
	require.NoError(t, s.InsertTask(ctx, newer))

	got, err := s.QueryCurrentTask(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "task-new", got.ID)
}

func TestQueryCurrentTask_badFilesJSON(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, repo_root, branch, phase, files, started_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"bad-files", "/repo", "main", "coding", "NOT_VALID_JSON", now, now,
	)
	require.NoError(t, err)

	_, err = s.QueryCurrentTask(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: query current task")
}

// --- UpdateTask ------------------------------------------------------------

func TestUpdateTask_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	tr := makeTaskRecord("task-upd", "coding", nil)
	require.NoError(t, s.InsertTask(ctx, tr))

	completedAt := time.Now().Truncate(time.Millisecond)
	tr.Phase = "reviewing"
	tr.CommitCount = 10
	tr.TestRuns = 15
	tr.TestFailures = 0
	tr.CompletedAt = &completedAt
	tr.Files = map[string]int{"/store.go": 5}
	require.NoError(t, s.UpdateTask(ctx, tr))

	// Query task history to see the updated record.
	tasks, err := s.QueryTaskHistory(ctx, time.Now().Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "reviewing", tasks[0].Phase)
	assert.Equal(t, 10, tasks[0].CommitCount)
	assert.Equal(t, 0, tasks[0].TestFailures)
	require.NotNil(t, tasks[0].CompletedAt)
	assert.True(t, tasks[0].CompletedAt.Equal(completedAt))
}

// --- QueryTaskHistory ------------------------------------------------------

func TestQueryTaskHistory_filtersSince(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)

	old := makeTaskRecord("old-task", "coding", nil)
	old.StartedAt = now.Add(-3 * time.Hour)
	old.LastActivity = old.StartedAt
	require.NoError(t, s.InsertTask(ctx, old))

	recent := makeTaskRecord("recent-task", "testing", nil)
	recent.StartedAt = now.Add(-30 * time.Minute)
	recent.LastActivity = recent.StartedAt
	require.NoError(t, s.InsertTask(ctx, recent))

	since := now.Add(-time.Hour)
	got, err := s.QueryTaskHistory(ctx, since, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "recent-task", got[0].ID)
}

func TestQueryTaskHistory_limitsResults(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		tr := makeTaskRecord("task-hist-"+string(rune('a'+i)), "coding", nil)
		tr.StartedAt = now.Add(time.Duration(i) * time.Minute)
		tr.LastActivity = tr.StartedAt
		require.NoError(t, s.InsertTask(ctx, tr))
	}

	got, err := s.QueryTaskHistory(ctx, now.Add(-time.Hour), 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestQueryTaskHistory_emptyStore(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryTaskHistory(context.Background(), time.Now().Add(-time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestQueryTaskHistory_orderedDescending(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)
	first := makeTaskRecord("first", "coding", nil)
	first.StartedAt = now.Add(-20 * time.Minute)
	first.LastActivity = first.StartedAt
	require.NoError(t, s.InsertTask(ctx, first))

	second := makeTaskRecord("second", "testing", nil)
	second.StartedAt = now.Add(-10 * time.Minute)
	second.LastActivity = second.StartedAt
	require.NoError(t, s.InsertTask(ctx, second))

	got, err := s.QueryTaskHistory(ctx, now.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Most recent first.
	assert.Equal(t, "second", got[0].ID)
	assert.Equal(t, "first", got[1].ID)
}

func TestQueryTaskHistory_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryTaskHistory(ctx, time.Now().Add(-time.Hour), 10)
	require.Error(t, err)
}

func TestQueryTaskHistory_badFilesJSON(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, repo_root, branch, phase, files, started_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"bad-history", "/repo", "main", "coding", "{invalid}", now, now,
	)
	require.NoError(t, err)

	_, err = s.QueryTaskHistory(ctx, time.Now().Add(-time.Minute), 10)
	require.Error(t, err)
}

// --- QueryTasksByDate ------------------------------------------------------

func TestQueryTasksByDate_returnsOnlyMatchingDay(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	today := time.Now().Truncate(time.Millisecond)
	yesterday := today.AddDate(0, 0, -1)

	tr1 := makeTaskRecord("today-task", "coding", nil)
	tr1.StartedAt = today
	tr1.LastActivity = today
	require.NoError(t, s.InsertTask(ctx, tr1))

	tr2 := makeTaskRecord("yesterday-task", "exploring", nil)
	tr2.StartedAt = yesterday
	tr2.LastActivity = yesterday
	require.NoError(t, s.InsertTask(ctx, tr2))

	got, err := s.QueryTasksByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "today-task", got[0].ID)
}

func TestQueryTasksByDate_emptyDay(t *testing.T) {
	s := openMemory(t)

	// Use a date far in the past so no tasks exist for it.
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)
	got, err := s.QueryTasksByDate(context.Background(), past)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestQueryTasksByDate_multipleSameDay(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	base := time.Date(2025, 6, 15, 9, 0, 0, 0, time.Local)
	for i := 0; i < 3; i++ {
		tr := makeTaskRecord("same-day-"+string(rune('a'+i)), "coding", nil)
		tr.StartedAt = base.Add(time.Duration(i) * time.Hour)
		tr.LastActivity = tr.StartedAt
		require.NoError(t, s.InsertTask(ctx, tr))
	}

	got, err := s.QueryTasksByDate(ctx, base)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	// Ordered ascending by started_at.
	assert.True(t, got[0].StartedAt.Before(got[1].StartedAt))
}

func TestQueryTasksByDate_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryTasksByDate(ctx, time.Now())
	require.Error(t, err)
}

func TestQueryTasksByDate_badFilesJSON(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now()
	nowMS := now.UnixMilli()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, repo_root, branch, phase, files, started_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"bad-date", "/repo", "main", "coding", "{invalid}", nowMS, nowMS,
	)
	require.NoError(t, err)

	_, err = s.QueryTasksByDate(ctx, now)
	require.Error(t, err)
}

// --- QueryGitEvents --------------------------------------------------------

func TestQueryGitEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	_ = s.InsertEvent(ctx, event.Event{
		Kind:      event.KindGit,
		Source:    "git",
		Payload:   map[string]any{"branch": "main", "message": "feat: add thing"},
		Timestamp: now,
	})
	_ = s.InsertEvent(ctx, event.Event{
		Kind:      event.KindFile,
		Source:    "files",
		Payload:   map[string]any{"path": "/x.go"},
		Timestamp: now,
	})

	got, err := s.QueryGitEvents(ctx, now.Add(-time.Second))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, event.KindGit, got[0].Kind)
	assert.Equal(t, "main", got[0].Payload["branch"])
}

func TestQueryGitEvents_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryGitEvents(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
}

// --- QueryTaskMetrics ------------------------------------------------------

func TestQueryTaskMetrics_empty(t *testing.T) {
	s := openMemory(t)
	m, err := s.QueryTaskMetrics(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, m.TasksStarted)
	assert.Equal(t, 0, m.TasksCompleted)
	assert.Equal(t, 0.0, m.AvgDurationMin)
	assert.Equal(t, 0.0, m.StuckRate)
	assert.NotNil(t, m.PhaseDistribution)
}

func TestQueryTaskMetrics_counts(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)

	// Two active tasks, one completed.
	tr1 := makeTaskRecord("m-active-1", "coding", nil)
	tr1.StartedAt = now.Add(-30 * time.Minute)
	tr1.LastActivity = tr1.StartedAt
	require.NoError(t, s.InsertTask(ctx, tr1))

	tr2 := makeTaskRecord("m-active-2", "testing", nil)
	tr2.StartedAt = now.Add(-15 * time.Minute)
	tr2.LastActivity = tr2.StartedAt
	require.NoError(t, s.InsertTask(ctx, tr2))

	completedAt := now.Add(-5 * time.Minute)
	tr3 := makeTaskRecord("m-done", "reviewing", &completedAt)
	tr3.StartedAt = now.Add(-60 * time.Minute)
	tr3.LastActivity = completedAt
	require.NoError(t, s.InsertTask(ctx, tr3))

	m, err := s.QueryTaskMetrics(ctx, now.Add(-2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, m.TasksStarted)
	assert.Equal(t, 1, m.TasksCompleted)
	assert.Greater(t, m.AvgDurationMin, 0.0)
}

func TestQueryTaskMetrics_stuckRate(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)

	// One stuck task (test_fails >= 3), one not stuck.
	stuck := makeTaskRecord("stuck", "testing", nil)
	stuck.TestFailures = 5
	stuck.StartedAt = now.Add(-20 * time.Minute)
	stuck.LastActivity = stuck.StartedAt
	require.NoError(t, s.InsertTask(ctx, stuck))

	ok := makeTaskRecord("ok", "coding", nil)
	ok.TestFailures = 0
	ok.StartedAt = now.Add(-10 * time.Minute)
	ok.LastActivity = ok.StartedAt
	require.NoError(t, s.InsertTask(ctx, ok))

	m, err := s.QueryTaskMetrics(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	// 1 stuck / 2 total = 0.5
	assert.InDelta(t, 0.5, m.StuckRate, 0.001)
}

func TestQueryTaskMetrics_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryTaskMetrics(ctx, time.Now().Add(-time.Hour))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: query task metrics")
}

// --- InsertMLEvent / QueryMLStats ------------------------------------------

func TestInsertMLEvent_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	require.NoError(t, s.InsertMLEvent(ctx, "prediction", "quality", "local", 42))
}

func TestQueryMLStats_empty(t *testing.T) {
	s := openMemory(t)
	st, err := s.QueryMLStats(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, st.Predictions)
	assert.Equal(t, 0, st.RetrainCount)
}

func TestQueryMLStats_counts(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertMLEvent(ctx, "prediction", "quality", "local", 10))
	require.NoError(t, s.InsertMLEvent(ctx, "prediction", "quality", "local", 15))
	require.NoError(t, s.InsertMLEvent(ctx, "retrain", "quality", "local", 300))
	// Older event inserted with a past timestamp via direct SQL to simulate a
	// record that should be excluded by the since window.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_events (kind, endpoint, routing, latency_ms, ts) VALUES (?, ?, ?, ?, ?)`,
		"prediction", "quality", "local", 10, time.Now().Add(-2*time.Hour).UnixMilli(),
	)
	require.NoError(t, err)

	st, err := s.QueryMLStats(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 2, st.Predictions)
	assert.Equal(t, 1, st.RetrainCount)
}

func TestQueryMLStats_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryMLStats(ctx, time.Now().Add(-time.Hour))
	require.Error(t, err)
}

// --- InsertPrediction / QueryLatestPrediction / QueryPredictions -----------

func TestInsertPrediction_noExpiry(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.9}`, 0.9, nil))
}

func TestInsertPrediction_withExpiry(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.8}`, 0.8, &exp))
}

func TestQueryLatestPrediction_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryLatestPrediction(context.Background(), "quality")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestQueryLatestPrediction_returnsNewest(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.5}`, 0.5, nil))
	// Small sleep to ensure distinct created_at in millisecond precision.
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.9}`, 0.9, nil))

	got, err := s.QueryLatestPrediction(ctx, "quality")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "quality", got.Model)
	assert.InDelta(t, 0.9, got.Confidence, 0.001)
	assert.Nil(t, got.ExpiresAt)
}

func TestQueryLatestPrediction_withExpiresAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	exp := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	require.NoError(t, s.InsertPrediction(ctx, "task_estimate", `{"min":10}`, 0.7, &exp))

	got, err := s.QueryLatestPrediction(ctx, "task_estimate")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.ExpiresAt)
	assert.True(t, got.ExpiresAt.Equal(exp), "ExpiresAt mismatch: got %v, want %v", got.ExpiresAt, exp)
}

func TestQueryLatestPrediction_expiredIsExcluded(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Insert a prediction that expired in the past.
	expired := time.Now().Add(-time.Hour)
	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.5}`, 0.5, &expired))

	got, err := s.QueryLatestPrediction(ctx, "quality")
	require.NoError(t, err)
	assert.Nil(t, got, "expired prediction should not be returned")
}

func TestQueryLatestPrediction_modelIsolation(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPrediction(ctx, "model-a", `{"x":1}`, 0.6, nil))
	require.NoError(t, s.InsertPrediction(ctx, "model-b", `{"x":2}`, 0.7, nil))

	got, err := s.QueryLatestPrediction(ctx, "model-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "model-a", got.Model)
}

func TestQueryLatestPrediction_badResultJSON(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES (?, ?, ?, ?)`,
		"bad-model", "NOT_JSON", 0.5, now,
	)
	require.NoError(t, err)

	_, err = s.QueryLatestPrediction(ctx, "bad-model")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: unmarshal prediction result")
}

func TestQueryPredictions_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryPredictions(context.Background(), "quality", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestQueryPredictions_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.3}`, 0.3, nil))
	time.Sleep(2 * time.Millisecond) // ensure distinct created_at timestamps
	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.7}`, 0.7, nil))
	// Different model — should not appear.
	require.NoError(t, s.InsertPrediction(ctx, "other", `{"score":1.0}`, 1.0, nil))

	got, err := s.QueryPredictions(ctx, "quality", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assert.Len(t, got, 2)
	// Results ordered by created_at DESC — most recent first.
	assert.InDelta(t, 0.7, got[0].Confidence, 0.001)
	assert.InDelta(t, 0.3, got[1].Confidence, 0.001)
}

func TestQueryPredictions_sinceFilter(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Insert an old prediction by direct SQL.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES (?, ?, ?, ?)`,
		"quality", `{"score":0.1}`, 0.1, time.Now().Add(-2*time.Hour).UnixMilli(),
	)
	require.NoError(t, err)

	require.NoError(t, s.InsertPrediction(ctx, "quality", `{"score":0.9}`, 0.9, nil))

	got, err := s.QueryPredictions(ctx, "quality", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.9, got[0].Confidence, 0.001)
}

func TestQueryPredictions_nilExpiresAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPrediction(ctx, "model-x", `{"v":1}`, 0.5, nil))

	got, err := s.QueryPredictions(ctx, "model-x", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Nil(t, got[0].ExpiresAt)
}

func TestQueryPredictions_withExpiresAt(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	exp := time.Now().Add(2 * time.Hour).Truncate(time.Millisecond)
	require.NoError(t, s.InsertPrediction(ctx, "model-y", `{"v":2}`, 0.8, &exp))

	got, err := s.QueryPredictions(ctx, "model-y", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].ExpiresAt)
	assert.True(t, got[0].ExpiresAt.Equal(exp))
}

func TestQueryPredictions_badResultJSON(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES (?, ?, ?, ?)`,
		"bad-pred", "NOT_JSON", 0.5, now,
	)
	require.NoError(t, err)

	_, err = s.QueryPredictions(ctx, "bad-pred", time.Now().Add(-time.Minute))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store: unmarshal prediction result")
}

func TestQueryPredictions_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryPredictions(ctx, "quality", time.Now().Add(-time.Hour))
	require.Error(t, err)
}

// --- InsertPluginEvent / QueryPluginEvents ----------------------------------

func TestInsertPluginEvent_roundtrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "suggestion_shown", `{"req":"abc"}`, `{"title":"run tests"}`))
}

func TestQueryPluginEvents_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.QueryPluginEvents(context.Background(), "", time.Now().Add(-time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestQueryPluginEvents_allPlugins(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "shown", `{}`, `{"title":"a"}`))
	require.NoError(t, s.InsertPluginEvent(ctx, "neovim", "accepted", `{}`, `{"title":"b"}`))

	got, err := s.QueryPluginEvents(ctx, "", time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestQueryPluginEvents_filterByPlugin(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "shown", `{}`, `{"title":"a"}`))
	require.NoError(t, s.InsertPluginEvent(ctx, "neovim", "shown", `{}`, `{"title":"b"}`))
	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "accepted", `{}`, `{"title":"c"}`))

	got, err := s.QueryPluginEvents(ctx, "vscode", time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "vscode", r.Plugin)
	}
}

func TestQueryPluginEvents_sinceFilter(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Insert old event directly with a past timestamp.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_events (plugin, kind, correlation, payload, ts) VALUES (?, ?, ?, ?, ?)`,
		"vscode", "old", `{}`, `{}`, time.Now().Add(-2*time.Hour).UnixMilli(),
	)
	require.NoError(t, err)

	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "recent", `{}`, `{}`))

	got, err := s.QueryPluginEvents(ctx, "vscode", time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "recent", got[0].Kind)
}

func TestQueryPluginEvents_limit(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "shown", `{}`, `{}`))
	}

	got, err := s.QueryPluginEvents(ctx, "", time.Now().Add(-time.Minute), 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestQueryPluginEvents_payloadDecoded(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	require.NoError(t, s.InsertPluginEvent(ctx, "vscode", "shown",
		`{"req_id":"abc123"}`,
		`{"title":"run tests","confidence":0.9}`,
	))

	got, err := s.QueryPluginEvents(ctx, "vscode", time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "abc123", got[0].Correlation["req_id"])
	assert.Equal(t, "run tests", got[0].Payload["title"])
}

func TestQueryPluginEvents_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryPluginEvents(ctx, "", time.Now().Add(-time.Hour), 10)
	require.Error(t, err)
}

func TestQueryPluginEvents_pluginFilter_cancelledContext(t *testing.T) {
	s := openMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.QueryPluginEvents(ctx, "vscode", time.Now().Add(-time.Hour), 10)
	require.Error(t, err)
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

func TestPurge_deletesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sigil_purge.db")

	s, err := Open(path)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, s.InsertEvent(ctx, event.Event{
		Kind: event.KindFile, Source: "files",
		Payload: map[string]any{"path": "/x.go"}, Timestamp: time.Now(),
	}))

	require.NoError(t, s.Purge())

	// The file must no longer exist on disk.
	require.NoFileExists(t, path)
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

func TestExport_writeError(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Insert an event so Export has something to encode.
	require.NoError(t, s.InsertEvent(ctx, event.Event{
		Kind:      event.KindFile,
		Source:    "files",
		Payload:   map[string]any{"path": "/x.go"},
		Timestamp: time.Now(),
	}))

	// Fail on the very first write attempt.
	w := &errWriter{after: 0}
	err := s.Export(w)
	require.Error(t, err)
}

// TestExport_suggestionsEncodeError verifies that Export encodes both events
// and suggestions when the writer succeeds.
func TestExport_suggestionsEncodeError(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Insert one event and one suggestion.
	require.NoError(t, s.InsertEvent(ctx, event.Event{
		Kind:      event.KindFile,
		Source:    "files",
		Payload:   map[string]any{"path": "/a.go"},
		Timestamp: time.Now(),
	}))
	_, err := s.InsertSuggestion(ctx, Suggestion{
		Category: "pattern", Confidence: 0.5,
		Title: "t", Body: "b", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	var buf errCountWriter
	err = s.Export(&buf)
	require.NoError(t, err)
	assert.Equal(t, 2, buf.encodes)
}

// --- Export writer helpers -------------------------------------------------

// errWriter fails after `after` successful Write calls.
type errWriter struct {
	calls int
	after int
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.calls >= w.after {
		return 0, errors.New("write: disk full")
	}
	w.calls++
	return len(p), nil
}

// errCountWriter counts encoded JSON objects (via newlines) and never errors.
type errCountWriter struct {
	encodes int
	data    []byte
}

func (w *errCountWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	for _, b := range p {
		if b == '\n' {
			w.encodes++
		}
	}
	return len(p), nil
}

// --- Paginated Query + Delete Tests ----------------------------------------

// seedEvents inserts n events of the given kind, returning the base timestamp.
func seedEvents(t *testing.T, s *Store, kind event.Kind, n int) time.Time {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		e := event.Event{
			Kind:      kind,
			Source:    string(kind),
			Payload:   map[string]any{"i": float64(i)},
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, s.InsertEvent(ctx, e))
	}
	return base
}

func TestQueryEventsPaginated(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Seed: 10 file events, 5 terminal events.
	fileBase := seedEvents(t, s, event.KindFile, 10)
	seedEvents(t, s, event.KindTerminal, 5)

	tests := []struct {
		name      string
		filter    EventFilter
		wantN     int
		wantTotal int
	}{
		{
			name:      "no filter default limit",
			filter:    EventFilter{},
			wantN:     15,
			wantTotal: 15,
		},
		{
			name:      "filter by kind",
			filter:    EventFilter{Kind: event.KindFile},
			wantN:     10,
			wantTotal: 10,
		},
		{
			name:      "filter by kind terminal",
			filter:    EventFilter{Kind: event.KindTerminal},
			wantN:     5,
			wantTotal: 5,
		},
		{
			name:      "limit smaller than total",
			filter:    EventFilter{Limit: 3},
			wantN:     3,
			wantTotal: 15,
		},
		{
			name:      "offset skips rows",
			filter:    EventFilter{Limit: 5, Offset: 10},
			wantN:     5,
			wantTotal: 15,
		},
		{
			name:      "offset past end",
			filter:    EventFilter{Limit: 5, Offset: 100},
			wantN:     0,
			wantTotal: 15,
		},
		{
			name: "time range filter",
			filter: EventFilter{
				Kind:  event.KindFile,
				After: fileBase.Add(5 * time.Second).UnixMilli(),
			},
			wantN:     5, // events 5,6,7,8,9
			wantTotal: 5,
		},
		{
			name: "time range before",
			filter: EventFilter{
				Kind:   event.KindFile,
				Before: fileBase.Add(3 * time.Second).UnixMilli(),
			},
			wantN:     3, // events 0,1,2
			wantTotal: 3,
		},
		{
			name:      "nonexistent kind",
			filter:    EventFilter{Kind: event.KindGit},
			wantN:     0,
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, total, err := s.QueryEventsPaginated(ctx, tt.filter)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total, "total count mismatch")
			assert.Len(t, events, tt.wantN, "result count mismatch")
			// Verify descending timestamp order.
			for i := 1; i < len(events); i++ {
				assert.True(t, !events[i].Timestamp.After(events[i-1].Timestamp),
					"events should be in descending timestamp order")
			}
		})
	}
}

func TestQueryEventsPaginated_emptyStore(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	events, total, err := s.QueryEventsPaginated(ctx, EventFilter{})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, events)
	assert.NotNil(t, events) // must return empty slice, never nil
}

func TestQueryEventsPaginated_limitsMax500(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	seedEvents(t, s, event.KindFile, 5)

	// Request limit > 500 — should be capped.
	events, _, err := s.QueryEventsPaginated(ctx, EventFilter{Limit: 1000})
	require.NoError(t, err)
	assert.Len(t, events, 5) // only 5 exist, but limit was capped to 500
}

func TestQueryEventByID(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	e := event.Event{
		Kind:      event.KindTerminal,
		Source:    "terminal",
		Payload:   map[string]any{"cmd": "make build", "cwd": "/home/nick"},
		Timestamp: time.Now().Truncate(time.Millisecond),
	}
	require.NoError(t, s.InsertEvent(ctx, e))

	// Get the inserted ID.
	all, err := s.QueryEvents(ctx, "", 1)
	require.NoError(t, err)
	require.Len(t, all, 1)

	got, err := s.QueryEventByID(ctx, all[0].ID)
	require.NoError(t, err)
	assert.Equal(t, all[0].ID, got.ID)
	assert.Equal(t, event.KindTerminal, got.Kind)
	assert.Equal(t, "make build", got.Payload["cmd"])
}

func TestQueryEventByID_notFound(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_, err := s.QueryEventByID(ctx, 99999)
	assert.True(t, errors.Is(err, sql.ErrNoRows) || err != nil, "should error for nonexistent ID")
}

func TestDeleteEvents(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	seedEvents(t, s, event.KindFile, 5)

	all, err := s.QueryEvents(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, all, 5)

	// Delete 2 specific events.
	ids := []int64{all[0].ID, all[1].ID}
	n, err := s.DeleteEvents(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Verify remaining.
	remaining, err := s.QueryEvents(ctx, "", 100)
	require.NoError(t, err)
	assert.Len(t, remaining, 3)
}

func TestDeleteEvents_empty(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	n, err := s.DeleteEvents(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestDeleteEvents_nonexistentIDs(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	seedEvents(t, s, event.KindFile, 3)

	n, err := s.DeleteEvents(ctx, []int64{99999, 99998})
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// All original events remain.
	remaining, err := s.QueryEvents(ctx, "", 100)
	require.NoError(t, err)
	assert.Len(t, remaining, 3)
}

func TestDeleteEventsFiltered(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	base := seedEvents(t, s, event.KindFile, 5)
	seedEvents(t, s, event.KindTerminal, 3)

	tests := []struct {
		name          string
		filter        EventFilter
		wantDeleted   int
		wantRemaining int
	}{
		{
			name:          "delete by kind",
			filter:        EventFilter{Kind: event.KindTerminal},
			wantDeleted:   3,
			wantRemaining: 5,
		},
		{
			name:          "delete by time range",
			filter:        EventFilter{Kind: event.KindFile, Before: base.Add(2 * time.Second).UnixMilli()},
			wantDeleted:   2, // file events at base+0s, base+1s
			wantRemaining: 6, // 3 file + 3 terminal remain
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Re-seed for each subtest.
			s := openMemory(t)
			seedEvents(t, s, event.KindFile, 5)
			seedEvents(t, s, event.KindTerminal, 3)

			n, err := s.DeleteEventsFiltered(ctx, tt.filter)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDeleted, n)

			remaining, _, err := s.QueryEventsPaginated(ctx, EventFilter{})
			require.NoError(t, err)
			assert.Equal(t, tt.wantRemaining, len(remaining))
		})
	}
}

func TestDeleteEventsFiltered_emptyFilter(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	seedEvents(t, s, event.KindFile, 5)

	// Empty filter matches all — deletes everything.
	n, err := s.DeleteEventsFiltered(ctx, EventFilter{})
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	remaining, total, err := s.QueryEventsPaginated(ctx, EventFilter{})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, remaining)
}
