// Package store provides the local SQLite persistence layer for aetherd.
// All raw telemetry is stored here and never leaves the machine.
// The store is opened in WAL mode to allow the analyzer to read while
// the collector writes.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wambozi/aether/internal/event"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps a SQLite database and exposes typed read/write methods.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs the schema
// migrations.  The caller is responsible for calling Close.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// Single writer, multiple readers — optimal for our access pattern.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertEvent persists a raw observation event.
func (s *Store) InsertEvent(ctx context.Context, e event.Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("store: marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO events (kind, source, payload, ts) VALUES (?, ?, ?, ?)`,
		string(e.Kind),
		e.Source,
		string(payload),
		e.Timestamp.UnixMilli(),
	)
	return err
}

// InsertAIInteraction persists a single AI interaction record.
func (s *Store) InsertAIInteraction(ctx context.Context, ai event.AIInteraction) error {
	accepted := 0
	if ai.Accepted {
		accepted = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_interactions
		 (query_text, query_category, routing, latency_ms, accepted, ts)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ai.QueryText,
		ai.QueryCategory,
		ai.Routing,
		ai.LatencyMS,
		accepted,
		ai.Timestamp.UnixMilli(),
	)
	return err
}

// QueryEvents returns the most recent n events, optionally filtered by kind.
// Pass an empty string for kind to return all events.
func (s *Store) QueryEvents(ctx context.Context, kind event.Kind, n int) ([]event.Event, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if kind == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kind, source, payload, ts FROM events ORDER BY ts DESC LIMIT ?`, n)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kind, source, payload, ts FROM events WHERE kind = ? ORDER BY ts DESC LIMIT ?`,
			string(kind), n)
	}
	if err != nil {
		return nil, fmt.Errorf("store: query events: %w", err)
	}
	defer rows.Close()

	var events []event.Event
	for rows.Next() {
		var (
			e       event.Event
			payload string
			tsMS    int64
		)
		if err := rows.Scan(&e.ID, (*string)(&e.Kind), &e.Source, &payload, &tsMS); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, err
		}
		e.Timestamp = time.UnixMilli(tsMS)
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountEvents returns the total number of stored events, optionally filtered
// by kind and a start time.  Used by the analyzer for frequency scoring.
func (s *Store) CountEvents(ctx context.Context, kind event.Kind, since time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE kind = ? AND ts >= ?`,
		string(kind), since.UnixMilli(),
	).Scan(&count)
	return count, err
}

// InsertPattern writes (or replaces) an analyzer-derived pattern.
func (s *Store) InsertPattern(ctx context.Context, kind string, summary any) error {
	blob, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("store: marshal pattern: %w", err)
	}
	now := time.Now().UnixMilli()

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO patterns (kind, summary, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(kind) DO UPDATE SET summary = excluded.summary, updated_at = excluded.updated_at`,
		kind, string(blob), now, now,
	)
	return err
}

// migrate creates all tables and indexes if they do not already exist.
// Idempotent — safe to call on every startup.
func migrate(db *sql.DB) error {
	schema := `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    kind    TEXT    NOT NULL,
    source  TEXT    NOT NULL,
    payload TEXT    NOT NULL,
    ts      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events (kind);
CREATE INDEX IF NOT EXISTS idx_events_ts   ON events (ts);

CREATE TABLE IF NOT EXISTS ai_interactions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    query_text     TEXT,
    query_category TEXT,
    routing        TEXT    NOT NULL,
    latency_ms     INTEGER NOT NULL,
    accepted       INTEGER NOT NULL DEFAULT 0,
    ts             INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_ts ON ai_interactions (ts);

CREATE TABLE IF NOT EXISTS patterns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL UNIQUE,
    summary    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);`

	_, err := db.Exec(schema)
	return err
}
