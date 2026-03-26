package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// GetSyncCursor returns the last synced row ID for a table.
func (s *Store) GetSyncCursor(ctx context.Context, table string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT last_synced_id FROM sync_cursors WHERE table_name = ?", table).Scan(&id)
	if err != nil {
		return 0, nil // no cursor = start from beginning
	}
	return id, nil
}

// SetSyncCursor updates the sync cursor for a table.
func (s *Store) SetSyncCursor(ctx context.Context, table string, lastID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_cursors (table_name, last_synced_id, last_synced_at)
		VALUES (?, ?, ?)
		ON CONFLICT(table_name) DO UPDATE SET last_synced_id = excluded.last_synced_id, last_synced_at = excluded.last_synced_at`,
		table, lastID, time.Now().Unix())
	return err
}

// syncAllowedTables is the set of tables that the sync agent is permitted to query.
var syncAllowedTables = map[string]bool{
	"events":         true,
	"tasks":          true,
	"suggestions":    true,
	"ml_predictions": true,
	"ml_events":      true,
	"patterns":       true,
}

// QueryRowsSince returns rows from the given table with id > sinceID, up to limit.
// Only tables in the sync allowlist may be queried.
func (s *Store) QueryRowsSince(ctx context.Context, table string, sinceID int64, limit int) ([]json.RawMessage, int64, error) {
	if !syncAllowedTables[table] {
		return nil, 0, fmt.Errorf("table %q not in sync allowlist", table)
	}

	if limit <= 0 {
		limit = 100
	}

	// Safe because table name is checked against a static allowlist above.
	query := fmt.Sprintf("SELECT * FROM %s WHERE id > ? ORDER BY id ASC LIMIT ?", table)

	rows, err := s.db.QueryContext(ctx, query, sinceID, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query %s: %w", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, fmt.Errorf("columns %s: %w", table, err)
	}

	var result []json.RawMessage
	var maxID int64

	for rows.Next() {
		// Build a generic scan target for each column.
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, 0, fmt.Errorf("scan %s: %w", table, err)
		}

		// Build a JSON object from column names and values.
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}

		// Track the max id for cursor advancement.
		if id, ok := row["id"]; ok {
			switch v := id.(type) {
			case int64:
				if v > maxID {
					maxID = v
				}
			}
		}

		raw, err := json.Marshal(row)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal row %s: %w", table, err)
		}
		result = append(result, raw)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate %s: %w", table, err)
	}

	return result, maxID, nil
}
