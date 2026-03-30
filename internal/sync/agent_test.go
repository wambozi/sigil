package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"log/slog"
)

// memCursorStore is an in-memory CursorStore for testing.
type memCursorStore struct {
	cursors map[string]int64
}

func newMemCursorStore() *memCursorStore {
	return &memCursorStore{cursors: make(map[string]int64)}
}

func (m *memCursorStore) GetSyncCursor(_ context.Context, table string) (int64, error) {
	return m.cursors[table], nil
}

func (m *memCursorStore) SetSyncCursor(_ context.Context, table string, lastID int64) error {
	m.cursors[table] = lastID
	return nil
}

// memSyncReader returns canned rows for testing.
type memSyncReader struct {
	rows  map[string][]json.RawMessage
	maxID int64
}

func (m *memSyncReader) QueryRowsSince(_ context.Context, table string, _ int64, _ int) ([]json.RawMessage, int64, error) {
	rows := m.rows[table]
	if len(rows) == 0 {
		return nil, 0, nil
	}
	return rows, m.maxID, nil
}

func TestAgentDefaults(t *testing.T) {
	a := New(nil, nil, Config{}, slog.Default())
	if a.cfg.BatchSize != 100 {
		t.Errorf("default BatchSize = %d, want 100", a.cfg.BatchSize)
	}
	if a.cfg.PollInterval != 5*time.Second {
		t.Errorf("default PollInterval = %v, want 5s", a.cfg.PollInterval)
	}
	if len(a.cfg.Tables) != 6 {
		t.Errorf("default Tables count = %d, want 6", len(a.cfg.Tables))
	}
}

func TestAgentPauseResume(t *testing.T) {
	a := New(nil, nil, Config{}, slog.Default())
	if a.IsPaused() {
		t.Error("agent should not be paused initially")
	}
	a.Pause()
	if !a.IsPaused() {
		t.Error("agent should be paused after Pause()")
	}
	a.Resume()
	if a.IsPaused() {
		t.Error("agent should not be paused after Resume()")
	}
}

func TestAgentSyncTable(t *testing.T) {
	var received map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cursors := newMemCursorStore()
	reader := &memSyncReader{
		rows: map[string][]json.RawMessage{
			"events": {json.RawMessage(`{"id":1,"kind":"file"}`)},
		},
		maxID: 1,
	}

	a := New(reader, cursors, Config{
		APIURL:       srv.URL,
		APIKey:       "test-key",
		BatchSize:    50,
		PollInterval: time.Minute,
		Tables:       []string{"events"},
	}, slog.Default())

	if err := a.syncTable(context.Background(), "events"); err != nil {
		t.Fatalf("syncTable: %v", err)
	}

	// Verify cursor was advanced.
	cur, _ := cursors.GetSyncCursor(context.Background(), "events")
	if cur != 1 {
		t.Errorf("cursor = %d, want 1", cur)
	}

	// Verify payload.
	if received["table"] != "events" {
		t.Errorf("table = %v, want events", received["table"])
	}
}

func TestAgentSyncTableNoRows(t *testing.T) {
	reader := &memSyncReader{rows: map[string][]json.RawMessage{}}
	cursors := newMemCursorStore()

	a := New(reader, cursors, Config{
		APIURL: "http://should-not-be-called",
		Tables: []string{"events"},
	}, slog.Default())

	// Should be a no-op when there are no rows.
	if err := a.syncTable(context.Background(), "events"); err != nil {
		t.Fatalf("syncTable with no rows: %v", err)
	}
}

func TestAgentStatus(t *testing.T) {
	cursors := newMemCursorStore()
	_ = cursors.SetSyncCursor(context.Background(), "events", 42)

	a := New(nil, cursors, Config{
		APIURL:       "http://example.com",
		PollInterval: 10 * time.Second,
		Tables:       []string{"events"},
	}, slog.Default())

	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status["paused"] != false {
		t.Error("expected paused=false")
	}
	curMap := status["cursors"].(map[string]int64)
	if curMap["events"] != 42 {
		t.Errorf("cursor = %d, want 42", curMap["events"])
	}
}
