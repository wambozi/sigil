// Command ingest-service is the Cloud Ingest HTTP service.
// It receives batched event data from sync agents on developer laptops,
// authenticates via API key, creates per-tenant Postgres schemas mirroring
// SQLite structure, and inserts with idempotent deduplication.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := loadConfig()

	db, err := sql.Open("postgres", cfg.DBURL)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	mux := http.NewServeMux()
	h := &handlers{db: db, log: log, apiKey: cfg.APIKey}

	mux.HandleFunc("POST /api/v1/ingest", h.handleIngest)
	mux.HandleFunc("GET /healthz", h.handleHealthz)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("ingest service starting", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("ingest service stopped")
}

type config struct {
	ListenAddr string
	DBURL      string
	APIKey     string
}

func loadConfig() config {
	return config{
		ListenAddr: envOr("LISTEN_ADDR", ":8082"),
		DBURL:      os.Getenv("DATABASE_URL"),
		APIKey:     os.Getenv("API_KEY"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type handlers struct {
	db     *sql.DB
	log    *slog.Logger
	apiKey string
}

func (h *handlers) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *handlers) handleIngest(w http.ResponseWriter, r *http.Request) {
	// Auth
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != h.apiKey {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Parse tenant from header (set by API gateway or from API key lookup)
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Parse payload
	var payload struct {
		Table  string            `json:"table"`
		Cursor int64             `json:"cursor"`
		Rows   []json.RawMessage `json:"rows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}

	// Validate table name (allowlist)
	allowed := map[string]bool{
		"events":         true,
		"tasks":          true,
		"suggestions":    true,
		"ml_predictions": true,
		"ml_events":      true,
		"patterns":       true,
	}
	if !allowed[payload.Table] {
		http.Error(w, fmt.Sprintf(`{"error":"table %q not allowed"}`, payload.Table), http.StatusBadRequest)
		return
	}

	// Ensure tenant schema exists
	schema := fmt.Sprintf("tenant_%s", sanitize(tenantID))
	if err := h.ensureSchema(r.Context(), schema); err != nil {
		h.log.Error("ensure schema", "err", err, "tenant", tenantID)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Insert rows (idempotent — ON CONFLICT DO NOTHING)
	inserted, err := h.insertRows(r.Context(), schema, payload.Table, payload.Rows)
	if err != nil {
		h.log.Error("insert rows", "err", err, "tenant", tenantID, "table", payload.Table)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Update sync cursor
	if err := h.updateCursor(r.Context(), schema, payload.Table, payload.Cursor); err != nil {
		h.log.Warn("update cursor", "err", err)
	}

	h.log.Info("ingested", "tenant", tenantID, "table", payload.Table, "rows", inserted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "inserted": inserted})
}

func sanitize(s string) string {
	// Only allow alphanumeric and underscore
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (h *handlers) ensureSchema(ctx context.Context, schema string) error {
	_, err := h.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		return err
	}
	// Create tables mirroring SQLite structure
	tables := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.events (
			id BIGINT PRIMARY KEY,
			kind TEXT NOT NULL,
			source TEXT NOT NULL,
			payload JSONB,
			ts BIGINT NOT NULL
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.tasks (
			id TEXT PRIMARY KEY,
			repo_root TEXT,
			branch TEXT,
			phase TEXT,
			files JSONB,
			started_at BIGINT,
			last_active BIGINT,
			completed_at BIGINT
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.suggestions (
			id BIGINT PRIMARY KEY,
			category TEXT,
			confidence REAL,
			title TEXT,
			body TEXT,
			status TEXT,
			created_at BIGINT
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.ml_predictions (
			id BIGINT PRIMARY KEY,
			model TEXT,
			result JSONB,
			confidence REAL,
			created_at BIGINT,
			expires_at BIGINT
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.ml_events (
			id BIGINT PRIMARY KEY,
			kind TEXT,
			endpoint TEXT,
			routing TEXT,
			latency_ms BIGINT,
			ts BIGINT
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.patterns (
			id BIGINT PRIMARY KEY,
			kind TEXT,
			summary JSONB,
			created_at BIGINT,
			updated_at BIGINT
		)`, schema),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.sync_cursor (
			table_name TEXT PRIMARY KEY,
			last_received_id BIGINT NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL DEFAULT 0
		)`, schema),
	}
	for _, ddl := range tables {
		if _, err := h.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return nil
}

func (h *handlers) insertRows(ctx context.Context, schema, table string, rows []json.RawMessage) (int, error) {
	// This is simplified — a real implementation would use COPY or batch INSERT
	inserted := 0
	for _, row := range rows {
		var m map[string]any
		json.Unmarshal(row, &m)
		// Generic insert with ON CONFLICT DO NOTHING for idempotency
		// (simplified — production would use proper column mapping)
		data, _ := json.Marshal(m)
		_, err := h.db.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s.%s SELECT * FROM jsonb_populate_record(null::%s.%s, $1) ON CONFLICT DO NOTHING",
				schema, table, schema, table),
			data)
		if err != nil {
			h.log.Debug("insert row skip", "err", err)
			continue
		}
		inserted++
	}
	return inserted, nil
}

func (h *handlers) updateCursor(ctx context.Context, schema, table string, cursor int64) error {
	_, err := h.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s.sync_cursor (table_name, last_received_id, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (table_name) DO UPDATE
		SET last_received_id = GREATEST(%s.sync_cursor.last_received_id, excluded.last_received_id),
		    updated_at = excluded.updated_at`,
			schema, schema),
		table, cursor, time.Now().Unix())
	return err
}
