// Package store provides the local SQLite persistence layer for sigild.
// All raw telemetry is stored here and never leaves the machine.
// The store is opened in WAL mode to allow the analyzer to read while
// the collector writes.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps a SQLite database and exposes typed read/write methods.
type Store struct {
	db *sql.DB
}

// EventFilter specifies pagination and filtering criteria for event queries.
type EventFilter struct {
	Kind   event.Kind // optional; zero value = all kinds
	After  int64      // UnixMilli; 0 = no lower bound
	Before int64      // UnixMilli; 0 = no upper bound
	Limit  int        // default 100, max 500
	Offset int        // default 0
}

// EventReader is the read-only subset of Store used by the analyzer, fleet
// reporter, and pattern detectors.
type EventReader interface {
	QueryEvents(ctx context.Context, kind event.Kind, n int) ([]event.Event, error)
	QueryEventsPaginated(ctx context.Context, filter EventFilter) ([]event.Event, int, error)
	QueryEventByID(ctx context.Context, id int64) (event.Event, error)
	CountEvents(ctx context.Context, kind event.Kind, since time.Time) (int64, error)
	QueryTopFiles(ctx context.Context, since time.Time, n int) ([]FileEditCount, error)
	QueryTerminalEvents(ctx context.Context, since time.Time) ([]event.Event, error)
	QueryHyprlandEvents(ctx context.Context, since time.Time) ([]event.Event, error)
	QueryRecentFileEvents(ctx context.Context, since time.Time) ([]event.Event, error)
	QueryAIInteractions(ctx context.Context, since time.Time) ([]event.AIInteraction, error)
	QuerySuggestionAcceptanceRate(ctx context.Context, since time.Time) (float64, error)
	QueryResolvedSuggestionCount(ctx context.Context, since time.Time) (int64, error)
	QueryPattern(ctx context.Context, kind string, dest any) error
	QuerySuggestions(ctx context.Context, status SuggestionStatus, n int) ([]Suggestion, error)
	QueryUndoableActions(ctx context.Context) ([]ActionRecord, error)
	QueryCurrentTask(ctx context.Context) (*TaskRecord, error)
	QueryTaskHistory(ctx context.Context, since time.Time, limit int) ([]TaskRecord, error)
	QueryTasksByDate(ctx context.Context, date time.Time) ([]TaskRecord, error)
	QueryGitEvents(ctx context.Context, since time.Time) ([]event.Event, error)
	QueryTaskMetrics(ctx context.Context, since time.Time) (TaskMetrics, error)
	QueryMLStats(ctx context.Context, since time.Time) (MLStats, error)
	QueryLatestPrediction(ctx context.Context, model string) (*PredictionRecord, error)
	QueryPredictions(ctx context.Context, model string, since time.Time) ([]PredictionRecord, error)
	QueryPluginEvents(ctx context.Context, pluginName string, since time.Time, limit int) ([]PluginEventRecord, error)
}

// EventWriter is the write subset of Store used by the collector and notifier.
type EventWriter interface {
	InsertEvent(ctx context.Context, e event.Event) error
	InsertAIInteraction(ctx context.Context, ai event.AIInteraction) error
	InsertPattern(ctx context.Context, kind string, summary any) error
	InsertSuggestion(ctx context.Context, sg Suggestion) (int64, error)
	UpdateSuggestionStatus(ctx context.Context, id int64, status SuggestionStatus) error
	InsertFeedback(ctx context.Context, suggestionID int64, outcome string) error
	InsertAction(ctx context.Context, actionID, description, executeCmd, undoCmd string, createdAt, expiresAt time.Time) error
	MarkActionUndone(ctx context.Context, actionID string) error
	InsertTask(ctx context.Context, t TaskRecord) error
	UpdateTask(ctx context.Context, t TaskRecord) error
	InsertMLEvent(ctx context.Context, kind, endpoint, routing string, latencyMS int64) error
	InsertPrediction(ctx context.Context, model, result string, confidence float64, expiresAt *time.Time) error
	DeleteEvents(ctx context.Context, ids []int64) (int, error)
	DeleteEventsFiltered(ctx context.Context, filter EventFilter) (int, error)
}

// ReadWriter combines EventReader and EventWriter for components that need both.
type ReadWriter interface {
	EventReader
	EventWriter
}

// Compile-time assertion: *Store satisfies ReadWriter.
var _ ReadWriter = (*Store)(nil)

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

// TaskMetrics holds aggregated task lifecycle metrics for fleet reporting.
type TaskMetrics struct {
	TasksCompleted    int
	TasksStarted      int
	AvgDurationMin    float64
	StuckRate         float64
	PhaseDistribution map[string]float64 // phase → percentage of time
}

// MLStats holds aggregated ML usage metrics for fleet reporting.
type MLStats struct {
	Predictions  int
	RetrainCount int
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

	events := make([]event.Event, 0)
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

// QueryEventsPaginated returns events matching the filter with pagination.
// It returns the matching events, the total count of matching events, and any error.
func (s *Store) QueryEventsPaginated(ctx context.Context, filter EventFilter) ([]event.Event, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}

	where, args := buildEventWhere(filter)

	// Count total matching rows.
	var total int
	countSQL := "SELECT COUNT(*) FROM events" + where
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count paginated events: %w", err)
	}

	// Fetch the page.
	querySQL := "SELECT id, kind, source, payload, ts FROM events" + where + " ORDER BY ts DESC LIMIT ? OFFSET ?"
	pageArgs := append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, querySQL, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: query paginated events: %w", err)
	}
	defer rows.Close()

	events := make([]event.Event, 0)
	for rows.Next() {
		var (
			e       event.Event
			payload string
			tsMS    int64
		)
		if err := rows.Scan(&e.ID, (*string)(&e.Kind), &e.Source, &payload, &tsMS); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, 0, err
		}
		e.Timestamp = time.UnixMilli(tsMS)
		events = append(events, e)
	}
	return events, total, rows.Err()
}

// QueryEventByID returns a single event by its ID with full payload.
func (s *Store) QueryEventByID(ctx context.Context, id int64) (event.Event, error) {
	var (
		e       event.Event
		payload string
		tsMS    int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, source, payload, ts FROM events WHERE id = ?`, id,
	).Scan(&e.ID, (*string)(&e.Kind), &e.Source, &payload, &tsMS)
	if err != nil {
		return event.Event{}, fmt.Errorf("store: query event by id: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
		return event.Event{}, fmt.Errorf("store: unmarshal event payload: %w", err)
	}
	e.Timestamp = time.UnixMilli(tsMS)
	return e, nil
}

// DeleteEvents removes specific events by ID. Returns the count of deleted rows.
func (s *Store) DeleteEvents(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	res, err := s.db.ExecContext(ctx,
		"DELETE FROM events WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return 0, fmt.Errorf("store: delete events by id: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteEventsFiltered removes events matching filter criteria. Returns the count deleted.
func (s *Store) DeleteEventsFiltered(ctx context.Context, filter EventFilter) (int, error) {
	where, args := buildEventWhere(filter)
	res, err := s.db.ExecContext(ctx, "DELETE FROM events"+where, args...)
	if err != nil {
		return 0, fmt.Errorf("store: delete events filtered: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// buildEventWhere constructs a SQL WHERE clause and args from an EventFilter.
func buildEventWhere(filter EventFilter) (string, []any) {
	var conditions []string
	var args []any

	if filter.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, string(filter.Kind))
	}
	if filter.After > 0 {
		conditions = append(conditions, "ts >= ?")
		args = append(args, filter.After)
	}
	if filter.Before > 0 {
		conditions = append(conditions, "ts < ?")
		args = append(args, filter.Before)
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
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

// QueryPattern reads a single pattern by kind and unmarshals its summary JSON
// into dest. Returns sql.ErrNoRows if the pattern does not exist.
func (s *Store) QueryPattern(ctx context.Context, kind string, dest any) error {
	var blob string
	err := s.db.QueryRowContext(ctx,
		`SELECT summary FROM patterns WHERE kind = ?`, kind,
	).Scan(&blob)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(blob), dest)
}

// --- Suggestions -----------------------------------------------------------

// SuggestionStatus is the lifecycle state of a surfaced suggestion.
type SuggestionStatus string

const (
	StatusPending   SuggestionStatus = "pending"
	StatusShown     SuggestionStatus = "shown"
	StatusAccepted  SuggestionStatus = "accepted"
	StatusDismissed SuggestionStatus = "dismissed"
	StatusIgnored   SuggestionStatus = "ignored"
)

// Suggestion is a single insight produced by the analyzer and tracked through
// its full lifecycle (created → shown → accepted/dismissed/ignored).
type Suggestion struct {
	ID         int64            `json:"id,omitempty"`
	Category   string           `json:"category"`   // "pattern", "reminder", "optimization", "insight"
	Confidence float64          `json:"confidence"` // 0.0-1.0
	Title      string           `json:"title"`
	Body       string           `json:"body"`
	ActionCmd  string           `json:"action_cmd,omitempty"` // optional shell command
	Status     SuggestionStatus `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
	ShownAt    *time.Time       `json:"shown_at,omitempty"`
	ResolvedAt *time.Time       `json:"resolved_at,omitempty"`
}

// InsertSuggestion persists a new suggestion and returns its assigned ID.
func (s *Store) InsertSuggestion(ctx context.Context, sg Suggestion) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO suggestions (category, confidence, title, body, action_cmd, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sg.Category, sg.Confidence, sg.Title, sg.Body,
		sg.ActionCmd, string(StatusPending), sg.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert suggestion: %w", err)
	}
	return res.LastInsertId()
}

// UpdateSuggestionStatus advances a suggestion's status and records the
// shown_at / resolved_at timestamps where appropriate.
func (s *Store) UpdateSuggestionStatus(ctx context.Context, id int64, status SuggestionStatus) error {
	now := time.Now().UnixMilli()
	var err error
	switch status {
	case StatusShown:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ?, shown_at = ? WHERE id = ?`,
			string(status), now, id)
	case StatusAccepted, StatusDismissed, StatusIgnored:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ?, resolved_at = ? WHERE id = ?`,
			string(status), now, id)
	default:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ? WHERE id = ?`,
			string(status), id)
	}
	return err
}

// QuerySuggestions returns the most recent n suggestions, optionally filtered
// by status.  Pass an empty string to return all statuses.
func (s *Store) QuerySuggestions(ctx context.Context, status SuggestionStatus, n int) ([]Suggestion, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, category, confidence, title, body, action_cmd, status, created_at, shown_at, resolved_at
			 FROM suggestions ORDER BY created_at DESC LIMIT ?`, n)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, category, confidence, title, body, action_cmd, status, created_at, shown_at, resolved_at
			 FROM suggestions WHERE status = ? ORDER BY created_at DESC LIMIT ?`,
			string(status), n)
	}
	if err != nil {
		return nil, fmt.Errorf("store: query suggestions: %w", err)
	}
	defer rows.Close()

	out := make([]Suggestion, 0)
	for rows.Next() {
		var (
			sg           Suggestion
			actionCmd    sql.NullString
			createdAtMS  int64
			shownAtMS    sql.NullInt64
			resolvedAtMS sql.NullInt64
		)
		if err := rows.Scan(
			&sg.ID, &sg.Category, &sg.Confidence, &sg.Title, &sg.Body,
			&actionCmd, (*string)(&sg.Status),
			&createdAtMS, &shownAtMS, &resolvedAtMS,
		); err != nil {
			return nil, err
		}
		sg.ActionCmd = actionCmd.String
		sg.CreatedAt = time.UnixMilli(createdAtMS)
		if shownAtMS.Valid {
			t := time.UnixMilli(shownAtMS.Int64)
			sg.ShownAt = &t
		}
		if resolvedAtMS.Valid {
			t := time.UnixMilli(resolvedAtMS.Int64)
			sg.ResolvedAt = &t
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// --- Pattern query helpers -------------------------------------------------

// FileEditCount holds a file path and the number of times it was edited in a
// query window.
type FileEditCount struct {
	Path  string
	Count int64
}

// TaskRecord represents a tracked task in the store.
type TaskRecord struct {
	ID           string
	RepoRoot     string
	Branch       string
	Phase        string
	Files        map[string]int // path → edit count
	StartedAt    time.Time
	LastActivity time.Time
	CompletedAt  *time.Time
	CommitCount  int
	TestRuns     int
	TestFailures int
}

// QueryTopFiles returns the n most-edited files (kind="file") since the given
// time, ordered by edit count descending.  The path is extracted from the
// JSON payload field "path".  Rows whose payload cannot be decoded are
// silently skipped — a single malformed row should not abort the query.
func (s *Store) QueryTopFiles(ctx context.Context, since time.Time, n int) ([]FileEditCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM events WHERE kind = ? AND ts >= ? ORDER BY ts DESC`,
		string(event.KindFile), since.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query top files: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("store: scan top files row: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue // skip malformed row
		}
		path, _ := payload["path"].(string)
		if path == "" {
			continue
		}
		counts[path]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate top files: %w", err)
	}

	// Collect into a slice and sort by count descending.
	out := make([]FileEditCount, 0, len(counts))
	for path, count := range counts {
		out = append(out, FileEditCount{Path: path, Count: count})
	}
	// Insertion sort is fine here — top-5 from a moderate set.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Count > out[j-1].Count; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// QueryTerminalEvents returns all terminal events (kind="terminal") with a
// timestamp at or after since, ordered by timestamp ascending.
func (s *Store) QueryTerminalEvents(ctx context.Context, since time.Time) ([]event.Event, error) {
	return s.queryEventsSince(ctx, event.KindTerminal, since)
}

// QueryHyprlandEvents returns all Hyprland events (kind="hyprland") with a
// timestamp at or after since, ordered by timestamp ascending.
func (s *Store) QueryHyprlandEvents(ctx context.Context, since time.Time) ([]event.Event, error) {
	return s.queryEventsSince(ctx, event.KindHyprland, since)
}

// QueryRecentFileEvents returns all file events (kind="file") with a timestamp
// at or after since, ordered by timestamp ascending.
func (s *Store) QueryRecentFileEvents(ctx context.Context, since time.Time) ([]event.Event, error) {
	return s.queryEventsSince(ctx, event.KindFile, since)
}

// queryEventsSince is the shared implementation for time-windowed event queries.
func (s *Store) queryEventsSince(ctx context.Context, kind event.Kind, since time.Time) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, source, payload, ts FROM events
		 WHERE kind = ? AND ts >= ? ORDER BY ts ASC`,
		string(kind), since.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query %s events since: %w", kind, err)
	}
	defer rows.Close()

	var out []event.Event
	for rows.Next() {
		var (
			e       event.Event
			payload string
			tsMS    int64
		)
		if err := rows.Scan(&e.ID, (*string)(&e.Kind), &e.Source, &payload, &tsMS); err != nil {
			return nil, fmt.Errorf("store: scan %s event: %w", kind, err)
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, fmt.Errorf("store: unmarshal %s payload: %w", kind, err)
		}
		e.Timestamp = time.UnixMilli(tsMS)
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryAIInteractions returns AI interaction records since the given time, ordered ascending.
func (s *Store) QueryAIInteractions(ctx context.Context, since time.Time) ([]event.AIInteraction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, query_text, query_category, routing, latency_ms, accepted, ts
		 FROM ai_interactions WHERE ts >= ? ORDER BY ts ASC`,
		since.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query ai interactions: %w", err)
	}
	defer rows.Close()

	var out []event.AIInteraction
	for rows.Next() {
		var (
			ai       event.AIInteraction
			queryTxt sql.NullString
			queryCat sql.NullString
			accepted int
			tsMS     int64
		)
		if err := rows.Scan(&ai.ID, &queryTxt, &queryCat, &ai.Routing, &ai.LatencyMS, &accepted, &tsMS); err != nil {
			return nil, fmt.Errorf("store: scan ai interaction: %w", err)
		}
		ai.QueryText = queryTxt.String
		ai.QueryCategory = queryCat.String
		ai.Accepted = accepted != 0
		ai.Timestamp = time.UnixMilli(tsMS)
		out = append(out, ai)
	}
	return out, rows.Err()
}

// QuerySuggestionAcceptanceRate returns the ratio of accepted/(accepted+dismissed) suggestions
// since the given time. Returns 0.0 if no resolved suggestions exist.
func (s *Store) QuerySuggestionAcceptanceRate(ctx context.Context, since time.Time) (float64, error) {
	var accepted, total int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM suggestions WHERE status = 'accepted' AND resolved_at >= ?`,
		since.UnixMilli(),
	).Scan(&accepted)
	if err != nil {
		return 0, fmt.Errorf("store: query acceptance rate (accepted): %w", err)
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM suggestions WHERE status IN ('accepted','dismissed') AND resolved_at >= ?`,
		since.UnixMilli(),
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("store: query acceptance rate (total): %w", err)
	}
	if total == 0 {
		return 0, nil
	}
	return float64(accepted) / float64(total), nil
}

// QueryResolvedSuggestionCount returns the number of suggestions with status
// 'accepted' or 'dismissed' resolved since the given time.
func (s *Store) QueryResolvedSuggestionCount(ctx context.Context, since time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM suggestions WHERE status IN ('accepted','dismissed') AND resolved_at >= ?`,
		since.UnixMilli(),
	).Scan(&count)
	return count, err
}

// --- Action log ------------------------------------------------------------

// ActionRecord is a stored action from the action_log table.
type ActionRecord struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	ExecuteCmd  string     `json:"execute_cmd,omitempty"`
	UndoCmd     string     `json:"undo_cmd,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UndoneAt    *time.Time `json:"undone_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

// InsertAction persists an actuator action to the action log.
func (s *Store) InsertAction(ctx context.Context, actionID, description, executeCmd, undoCmd string, createdAt, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO action_log (action_id, description, execute_cmd, undo_cmd, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		actionID, description, executeCmd, undoCmd, createdAt.UnixMilli(), expiresAt.UnixMilli(),
	)
	return err
}

// QueryUndoableActions returns actions whose undo window has not expired and
// that have not been undone yet, ordered by created_at ascending.
func (s *Store) QueryUndoableActions(ctx context.Context) ([]ActionRecord, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.QueryContext(ctx,
		`SELECT action_id, description, execute_cmd, undo_cmd, created_at, expires_at
		 FROM action_log
		 WHERE expires_at > ? AND undone_at IS NULL
		 ORDER BY created_at ASC`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query undoable actions: %w", err)
	}
	defer rows.Close()

	var out []ActionRecord
	for rows.Next() {
		var (
			a           ActionRecord
			createdAtMS int64
			expiresAtMS int64
		)
		if err := rows.Scan(&a.ID, &a.Description, &a.ExecuteCmd, &a.UndoCmd, &createdAtMS, &expiresAtMS); err != nil {
			return nil, fmt.Errorf("store: scan action record: %w", err)
		}
		a.CreatedAt = time.UnixMilli(createdAtMS)
		a.ExpiresAt = time.UnixMilli(expiresAtMS)
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkActionUndone sets the undone_at timestamp for the given action.
func (s *Store) MarkActionUndone(ctx context.Context, actionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE action_log SET undone_at = ? WHERE action_id = ?`,
		time.Now().UnixMilli(), actionID,
	)
	return err
}

// --- Feedback --------------------------------------------------------------

// InsertFeedback records the outcome of a surfaced suggestion.
func (s *Store) InsertFeedback(ctx context.Context, suggestionID int64, outcome string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback (suggestion_id, outcome, ts) VALUES (?, ?, ?)`,
		suggestionID, outcome, time.Now().UnixMilli(),
	)
	return err
}

// --- Task tracking ---------------------------------------------------------

// InsertTask persists a new task record.
func (s *Store) InsertTask(ctx context.Context, t TaskRecord) error {
	files, err := json.Marshal(t.Files)
	if err != nil {
		return fmt.Errorf("store: marshal task files: %w", err)
	}

	var completedAt sql.NullInt64
	if t.CompletedAt != nil {
		completedAt = sql.NullInt64{Int64: t.CompletedAt.UnixMilli(), Valid: true}
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, repo_root, branch, phase, files, started_at, last_active, completed_at, commit_count, test_runs, test_fails)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.RepoRoot, t.Branch, t.Phase, string(files),
		t.StartedAt.UnixMilli(), t.LastActivity.UnixMilli(), completedAt,
		t.CommitCount, t.TestRuns, t.TestFailures,
	)
	return err
}

// UpdateTask updates an existing task record by ID.
func (s *Store) UpdateTask(ctx context.Context, t TaskRecord) error {
	files, err := json.Marshal(t.Files)
	if err != nil {
		return fmt.Errorf("store: marshal task files: %w", err)
	}

	var completedAt sql.NullInt64
	if t.CompletedAt != nil {
		completedAt = sql.NullInt64{Int64: t.CompletedAt.UnixMilli(), Valid: true}
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET repo_root = ?, branch = ?, phase = ?, files = ?,
		 started_at = ?, last_active = ?, completed_at = ?,
		 commit_count = ?, test_runs = ?, test_fails = ?
		 WHERE id = ?`,
		t.RepoRoot, t.Branch, t.Phase, string(files),
		t.StartedAt.UnixMilli(), t.LastActivity.UnixMilli(), completedAt,
		t.CommitCount, t.TestRuns, t.TestFailures, t.ID,
	)
	return err
}

// QueryCurrentTask returns the most recently active non-idle, non-completed task,
// or nil if no such task exists.
func (s *Store) QueryCurrentTask(ctx context.Context) (*TaskRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, repo_root, branch, phase, files, started_at, last_active, completed_at, commit_count, test_runs, test_fails
		 FROM tasks WHERE phase != 'idle' AND completed_at IS NULL
		 ORDER BY last_active DESC LIMIT 1`,
	)
	t, err := scanTaskRecord(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: query current task: %w", err)
	}
	return t, nil
}

// QueryTaskHistory returns tasks started at or after since, ordered by
// started_at descending, limited to limit rows.
func (s *Store) QueryTaskHistory(ctx context.Context, since time.Time, limit int) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_root, branch, phase, files, started_at, last_active, completed_at, commit_count, test_runs, test_fails
		 FROM tasks WHERE started_at >= ?
		 ORDER BY started_at DESC LIMIT ?`,
		since.UnixMilli(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query task history: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

// QueryTasksByDate returns all tasks whose started_at falls within the given
// calendar day (in the local timezone), ordered by started_at ascending.
func (s *Store) QueryTasksByDate(ctx context.Context, date time.Time) ([]TaskRecord, error) {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	startOfNext := startOfDay.AddDate(0, 0, 1)

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_root, branch, phase, files, started_at, last_active, completed_at, commit_count, test_runs, test_fails
		 FROM tasks WHERE started_at >= ? AND started_at < ?
		 ORDER BY started_at ASC`,
		startOfDay.UnixMilli(), startOfNext.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query tasks by date: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

// QueryGitEvents returns all git events since the given time.
func (s *Store) QueryGitEvents(ctx context.Context, since time.Time) ([]event.Event, error) {
	return s.queryEventsSince(ctx, event.KindGit, since)
}

// InsertMLEvent persists an ML event (prediction, retrain, etc.).
func (s *Store) InsertMLEvent(ctx context.Context, kind, endpoint, routing string, latencyMS int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_events (kind, endpoint, routing, latency_ms, ts) VALUES (?, ?, ?, ?, ?)`,
		kind, endpoint, routing, latencyMS, time.Now().UnixMilli(),
	)
	return err
}

// InsertPluginEvent persists a plugin event.
func (s *Store) InsertPluginEvent(ctx context.Context, plugin, kind, correlation, payload string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_events (plugin, kind, correlation, payload, ts) VALUES (?, ?, ?, ?, ?)`,
		plugin, kind, correlation, payload, time.Now().UnixMilli(),
	)
	return err
}

// QueryPluginEvents returns recent plugin events, optionally filtered by plugin name.
func (s *Store) QueryPluginEvents(ctx context.Context, pluginName string, since time.Time, limit int) ([]PluginEventRecord, error) {
	sinceMS := since.UnixMilli()
	var rows *sql.Rows
	var err error

	if pluginName == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, plugin, kind, correlation, payload, ts FROM plugin_events
			 WHERE ts >= ? ORDER BY ts DESC LIMIT ?`, sinceMS, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, plugin, kind, correlation, payload, ts FROM plugin_events
			 WHERE plugin = ? AND ts >= ? ORDER BY ts DESC LIMIT ?`, pluginName, sinceMS, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PluginEventRecord
	for rows.Next() {
		var r PluginEventRecord
		var tsMS int64
		var corr, payload string
		if err := rows.Scan(&r.ID, &r.Plugin, &r.Kind, &corr, &payload, &tsMS); err != nil {
			return nil, err
		}
		r.Timestamp = time.UnixMilli(tsMS)
		_ = json.Unmarshal([]byte(corr), &r.Correlation)
		_ = json.Unmarshal([]byte(payload), &r.Payload)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PluginEventRecord is a persisted plugin event.
type PluginEventRecord struct {
	ID          int64
	Plugin      string
	Kind        string
	Correlation map[string]string
	Payload     map[string]any
	Timestamp   time.Time
}

// --- ML Predictions --------------------------------------------------------

// InsertPrediction persists an ML prediction result.
func (s *Store) InsertPrediction(ctx context.Context, model, result string, confidence float64, expiresAt *time.Time) error {
	now := time.Now().UnixMilli()
	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.UnixMilli(), Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ml_predictions (model, result, confidence, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		model, result, confidence, now, expires,
	)
	return err
}

// QueryLatestPrediction returns the most recent non-expired prediction for the
// given model, or nil,nil if no matching row exists.
func (s *Store) QueryLatestPrediction(ctx context.Context, model string) (*PredictionRecord, error) {
	now := time.Now().UnixMilli()
	row := s.db.QueryRowContext(ctx,
		`SELECT id, model, result, confidence, created_at, expires_at
		 FROM ml_predictions
		 WHERE model = ? AND (expires_at IS NULL OR expires_at > ?)
		 ORDER BY created_at DESC LIMIT 1`,
		model, now,
	)

	var (
		p         PredictionRecord
		resultStr string
		createdMS int64
		expiresMS sql.NullInt64
	)
	if err := row.Scan(&p.ID, &p.Model, &resultStr, &p.Confidence, &createdMS, &expiresMS); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: query latest prediction: %w", err)
	}
	if err := json.Unmarshal([]byte(resultStr), &p.Result); err != nil {
		return nil, fmt.Errorf("store: unmarshal prediction result: %w", err)
	}
	p.CreatedAt = time.UnixMilli(createdMS)
	if expiresMS.Valid {
		t := time.UnixMilli(expiresMS.Int64)
		p.ExpiresAt = &t
	}
	return &p, nil
}

// QueryPredictions returns all predictions for the given model created at or
// after since, ordered by created_at descending.
func (s *Store) QueryPredictions(ctx context.Context, model string, since time.Time) ([]PredictionRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, model, result, confidence, created_at, expires_at
		 FROM ml_predictions
		 WHERE model = ? AND created_at >= ?
		 ORDER BY created_at DESC`,
		model, since.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query predictions: %w", err)
	}
	defer rows.Close()

	var out []PredictionRecord
	for rows.Next() {
		var (
			p         PredictionRecord
			resultStr string
			createdMS int64
			expiresMS sql.NullInt64
		)
		if err := rows.Scan(&p.ID, &p.Model, &resultStr, &p.Confidence, &createdMS, &expiresMS); err != nil {
			return nil, fmt.Errorf("store: scan prediction: %w", err)
		}
		if err := json.Unmarshal([]byte(resultStr), &p.Result); err != nil {
			return nil, fmt.Errorf("store: unmarshal prediction result: %w", err)
		}
		p.CreatedAt = time.UnixMilli(createdMS)
		if expiresMS.Valid {
			t := time.UnixMilli(expiresMS.Int64)
			p.ExpiresAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PredictionRecord represents a stored ML prediction.
type PredictionRecord struct {
	ID         int64
	Model      string
	Result     map[string]any
	Confidence float64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
}

// QueryTaskMetrics computes aggregated task lifecycle metrics since the given time.
func (s *Store) QueryTaskMetrics(ctx context.Context, since time.Time) (TaskMetrics, error) {
	sinceMS := since.UnixMilli()
	var m TaskMetrics

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE started_at >= ?`, sinceMS,
	).Scan(&m.TasksStarted)
	if err != nil {
		return m, fmt.Errorf("store: query task metrics (started): %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE completed_at IS NOT NULL AND completed_at >= ?`, sinceMS,
	).Scan(&m.TasksCompleted)
	if err != nil {
		return m, fmt.Errorf("store: query task metrics (completed): %w", err)
	}

	var avgDur sql.NullFloat64
	err = s.db.QueryRowContext(ctx,
		`SELECT AVG((last_active - started_at) / 60000.0) FROM tasks
		 WHERE completed_at IS NOT NULL AND completed_at >= ?`, sinceMS,
	).Scan(&avgDur)
	if err != nil {
		return m, fmt.Errorf("store: query task metrics (avg duration): %w", err)
	}
	if avgDur.Valid {
		m.AvgDurationMin = avgDur.Float64
	}

	// Approximate stuck rate: tasks with test_fails >= 3 / total started.
	var stuckCount int
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE started_at >= ? AND test_fails >= 3`, sinceMS,
	).Scan(&stuckCount)
	if err != nil {
		return m, fmt.Errorf("store: query task metrics (stuck): %w", err)
	}
	if m.TasksStarted > 0 {
		m.StuckRate = float64(stuckCount) / float64(m.TasksStarted)
	}

	// TODO: track phase time in task record to compute real distribution.
	m.PhaseDistribution = make(map[string]float64)

	return m, nil
}

// QueryMLStats computes aggregated ML usage metrics since the given time.
func (s *Store) QueryMLStats(ctx context.Context, since time.Time) (MLStats, error) {
	sinceMS := since.UnixMilli()
	var st MLStats

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ml_events WHERE kind = 'prediction' AND ts >= ?`, sinceMS,
	).Scan(&st.Predictions)
	if err != nil {
		return st, fmt.Errorf("store: query ml stats (predictions): %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ml_events WHERE kind = 'retrain' AND ts >= ?`, sinceMS,
	).Scan(&st.RetrainCount)
	if err != nil {
		return st, fmt.Errorf("store: query ml stats (retrain): %w", err)
	}

	return st, nil
}

// scanTaskRecord scans a single task row from a *sql.Row.
func scanTaskRecord(row *sql.Row) (*TaskRecord, error) {
	var (
		t           TaskRecord
		filesJSON   string
		startedMS   int64
		lastMS      int64
		completedMS sql.NullInt64
	)
	if err := row.Scan(
		&t.ID, &t.RepoRoot, &t.Branch, &t.Phase, &filesJSON,
		&startedMS, &lastMS, &completedMS,
		&t.CommitCount, &t.TestRuns, &t.TestFailures,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(filesJSON), &t.Files); err != nil {
		return nil, fmt.Errorf("store: unmarshal task files: %w", err)
	}
	t.StartedAt = time.UnixMilli(startedMS)
	t.LastActivity = time.UnixMilli(lastMS)
	if completedMS.Valid {
		ct := time.UnixMilli(completedMS.Int64)
		t.CompletedAt = &ct
	}
	return &t, nil
}

// scanTaskRows scans multiple task rows from *sql.Rows.
func scanTaskRows(rows *sql.Rows) ([]TaskRecord, error) {
	var out []TaskRecord
	for rows.Next() {
		var (
			t           TaskRecord
			filesJSON   string
			startedMS   int64
			lastMS      int64
			completedMS sql.NullInt64
		)
		if err := rows.Scan(
			&t.ID, &t.RepoRoot, &t.Branch, &t.Phase, &filesJSON,
			&startedMS, &lastMS, &completedMS,
			&t.CommitCount, &t.TestRuns, &t.TestFailures,
		); err != nil {
			return nil, fmt.Errorf("store: scan task row: %w", err)
		}
		if err := json.Unmarshal([]byte(filesJSON), &t.Files); err != nil {
			return nil, fmt.Errorf("store: unmarshal task files: %w", err)
		}
		t.StartedAt = time.UnixMilli(startedMS)
		t.LastActivity = time.UnixMilli(lastMS)
		if completedMS.Valid {
			ct := time.UnixMilli(completedMS.Int64)
			t.CompletedAt = &ct
		}
		out = append(out, t)
	}
	return out, rows.Err()
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
);

CREATE TABLE IF NOT EXISTS suggestions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    category    TEXT    NOT NULL,
    confidence  REAL    NOT NULL,
    title       TEXT    NOT NULL,
    body        TEXT    NOT NULL,
    action_cmd  TEXT,
    status      TEXT    NOT NULL DEFAULT 'pending',
    created_at  INTEGER NOT NULL,
    shown_at    INTEGER,
    resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_suggestions_status ON suggestions (status);

CREATE TABLE IF NOT EXISTS feedback (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    suggestion_id INTEGER NOT NULL REFERENCES suggestions(id),
    outcome       TEXT    NOT NULL,
    ts            INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS action_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    action_id   TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL,
    execute_cmd TEXT    NOT NULL DEFAULT '',
    undo_cmd    TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    undone_at   INTEGER,
    expires_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_action_log_created ON action_log (created_at);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT    PRIMARY KEY,
    repo_root    TEXT    NOT NULL,
    branch       TEXT    NOT NULL DEFAULT '',
    phase        TEXT    NOT NULL DEFAULT 'idle',
    files        TEXT    NOT NULL DEFAULT '{}',
    started_at   INTEGER NOT NULL,
    last_active  INTEGER NOT NULL,
    completed_at INTEGER,
    commit_count INTEGER NOT NULL DEFAULT 0,
    test_runs    INTEGER NOT NULL DEFAULT 0,
    test_fails   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tasks_phase ON tasks (phase);
CREATE INDEX IF NOT EXISTS idx_tasks_started ON tasks (started_at);

CREATE TABLE IF NOT EXISTS ml_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL,
    endpoint   TEXT    NOT NULL,
    routing    TEXT    NOT NULL,
    latency_ms INTEGER NOT NULL,
    ts         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ml_events_ts ON ml_events(ts);

CREATE TABLE IF NOT EXISTS ml_predictions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    model      TEXT NOT NULL,
    result     TEXT NOT NULL,
    confidence REAL NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_ml_predictions_model ON ml_predictions(model);
CREATE INDEX IF NOT EXISTS idx_ml_predictions_created ON ml_predictions(created_at);

CREATE TABLE IF NOT EXISTS plugin_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin      TEXT NOT NULL,
    kind        TEXT NOT NULL,
    correlation TEXT NOT NULL DEFAULT '{}',
    payload     TEXT NOT NULL DEFAULT '{}',
    ts          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plugin_events_plugin ON plugin_events(plugin);
CREATE INDEX IF NOT EXISTS idx_plugin_events_ts ON plugin_events(ts);

CREATE TABLE IF NOT EXISTS sync_cursors (
    table_name     TEXT PRIMARY KEY,
    last_synced_id INTEGER NOT NULL DEFAULT 0,
    last_synced_at INTEGER NOT NULL DEFAULT 0
);`

	_, err := db.Exec(schema)
	return err
}

// --- Privacy commands -------------------------------------------------------

// Purge deletes all rows from all tables and then removes the SQLite database
// file from disk.  The Store must not be used after calling Purge.
func (s *Store) Purge() error {
	tables := []string{"sync_cursors", "plugin_events", "ml_predictions", "ml_events", "tasks", "feedback", "suggestions", "patterns", "ai_interactions", "events"}
	for _, t := range tables {
		if _, err := s.db.Exec("DELETE FROM " + t); err != nil {
			return fmt.Errorf("store: purge table %s: %w", t, err)
		}
	}

	// Retrieve the database file path from the PRAGMA so we can remove it.
	var seq int
	var dbName, dbPath string
	row := s.db.QueryRow("PRAGMA database_list")
	_ = row.Scan(&seq, &dbName, &dbPath)

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close before purge: %w", err)
	}

	if dbPath != "" && dbPath != ":memory:" {
		if err := os.Remove(dbPath); err != nil {
			return fmt.Errorf("store: remove db file: %w", err)
		}
	}
	return nil
}

// Export writes all events and suggestions as newline-delimited JSON to w.
func (s *Store) Export(w io.Writer) error {
	ctx := context.Background()
	enc := json.NewEncoder(w)

	events, err := s.QueryEvents(ctx, "", 1<<30)
	if err != nil {
		return fmt.Errorf("store: export events: %w", err)
	}
	for _, e := range events {
		if err := enc.Encode(map[string]any{
			"type":      "event",
			"id":        e.ID,
			"kind":      e.Kind,
			"source":    e.Source,
			"payload":   e.Payload,
			"timestamp": e.Timestamp.UTC().Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("store: encode event: %w", err)
		}
	}

	suggestions, err := s.QuerySuggestions(ctx, "", 1<<30)
	if err != nil {
		return fmt.Errorf("store: export suggestions: %w", err)
	}
	for _, sg := range suggestions {
		if err := enc.Encode(map[string]any{
			"type":       "suggestion",
			"id":         sg.ID,
			"category":   sg.Category,
			"confidence": sg.Confidence,
			"title":      sg.Title,
			"body":       sg.Body,
			"action_cmd": sg.ActionCmd,
			"status":     sg.Status,
			"created_at": sg.CreatedAt.UTC().Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("store: encode suggestion: %w", err)
		}
	}
	return nil
}
