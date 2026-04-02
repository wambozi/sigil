// Package sync implements the Sync Agent that streams local SQLite changes
// to the cloud ingest API. It tracks per-table cursors and batches rows,
// only starting when explicitly enabled via configuration.
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// SyncReader provides read access to tables that the sync agent ships.
type SyncReader interface {
	QueryRowsSince(ctx context.Context, table string, sinceID int64, limit int) ([]json.RawMessage, int64, error)
}

// CursorStore persists per-table sync cursors.
type CursorStore interface {
	GetSyncCursor(ctx context.Context, table string) (int64, error)
	SetSyncCursor(ctx context.Context, table string, lastID int64) error
}

// Config holds sync agent configuration.
type Config struct {
	APIURL       string
	APIKey       string
	BatchSize    int
	PollInterval time.Duration
	Tables       []string
}

// Agent streams local SQLite changes to the cloud ingest API.
type Agent struct {
	reader  SyncReader
	cursors CursorStore
	cfg     Config
	log     *slog.Logger
	client  *http.Client
	paused  bool
}

// New creates a sync agent.
func New(reader SyncReader, cursors CursorStore, cfg Config, log *slog.Logger) *Agent {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if len(cfg.Tables) == 0 {
		cfg.Tables = []string{"events", "tasks", "suggestions", "ml_predictions", "ml_events", "patterns"}
	}
	return &Agent{
		reader:  reader,
		cursors: cursors,
		cfg:     cfg,
		log:     log,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Run starts the sync loop. Blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("sync agent started", "api_url", a.cfg.APIURL, "interval", a.cfg.PollInterval)
	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("sync agent stopped")
			return nil
		case <-ticker.C:
			if a.paused {
				continue
			}
			a.syncAll(ctx)
		}
	}
}

// Pause temporarily suspends syncing.
func (a *Agent) Pause() { a.paused = true }

// Resume restarts syncing after a pause.
func (a *Agent) Resume() { a.paused = false }

// IsPaused returns whether syncing is paused.
func (a *Agent) IsPaused() bool { return a.paused }

// Status returns a snapshot of per-table cursor positions and the paused flag.
func (a *Agent) Status(ctx context.Context) (map[string]any, error) {
	cursors := make(map[string]int64, len(a.cfg.Tables))
	for _, table := range a.cfg.Tables {
		cur, err := a.cursors.GetSyncCursor(ctx, table)
		if err != nil {
			return nil, fmt.Errorf("get cursor for %s: %w", table, err)
		}
		cursors[table] = cur
	}
	return map[string]any{
		"enabled":  true,
		"paused":   a.paused,
		"api_url":  a.cfg.APIURL,
		"interval": a.cfg.PollInterval.String(),
		"tables":   a.cfg.Tables,
		"cursors":  cursors,
	}, nil
}

func (a *Agent) syncAll(ctx context.Context) {
	for _, table := range a.cfg.Tables {
		if err := a.syncTable(ctx, table); err != nil {
			a.log.Warn("sync failed", "table", table, "err", err)
		}
	}
}

func (a *Agent) syncTable(ctx context.Context, table string) error {
	cursor, err := a.cursors.GetSyncCursor(ctx, table)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	rows, maxID, err := a.reader.QueryRowsSince(ctx, table, cursor, a.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("query rows: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	payload := map[string]any{
		"table":  table,
		"cursor": cursor,
		"rows":   rows,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", a.cfg.APIURL+"/ingest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ingest returned %d", resp.StatusCode)
	}

	if err := a.cursors.SetSyncCursor(ctx, table, maxID); err != nil {
		return fmt.Errorf("set cursor: %w", err)
	}

	a.log.Debug("synced", "table", table, "rows", len(rows), "cursor", maxID)
	return nil
}
