package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openDB creates a connection pool to PostgreSQL.
func openDB(url string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}

// migrateDB runs the schema DDL statements.
func migrateDB(pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := `
CREATE TABLE IF NOT EXISTS orgs (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS teams (
    id      SERIAL PRIMARY KEY,
    org_id  INTEGER NOT NULL REFERENCES orgs(id),
    name    TEXT NOT NULL,
    UNIQUE(org_id, name)
);

CREATE TABLE IF NOT EXISTS nodes (
    id          SERIAL PRIMARY KEY,
    node_id     TEXT NOT NULL UNIQUE,
    org_id      INTEGER NOT NULL REFERENCES orgs(id),
    team_id     INTEGER REFERENCES teams(id),
    enrolled_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS daily_metrics (
    id                      SERIAL PRIMARY KEY,
    node_id                 TEXT NOT NULL REFERENCES nodes(node_id),
    date                    DATE NOT NULL,
    ai_query_counts         JSONB NOT NULL DEFAULT '{}',
    suggestion_accept_rate  REAL NOT NULL DEFAULT 0,
    adoption_tier           SMALLINT NOT NULL DEFAULT 0,
    local_routing_ratio    REAL NOT NULL DEFAULT 0,
    build_success_rate      REAL NOT NULL DEFAULT 0,
    total_events            INTEGER NOT NULL DEFAULT 0,
    received_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(node_id, date)
);

CREATE INDEX IF NOT EXISTS idx_daily_metrics_date ON daily_metrics(date);
CREATE INDEX IF NOT EXISTS idx_daily_metrics_node ON daily_metrics(node_id);

CREATE TABLE IF NOT EXISTS policies (
    id          SERIAL PRIMARY KEY,
    org_id      INTEGER NOT NULL REFERENCES orgs(id),
    policy      JSONB NOT NULL,
    enforced_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_policies_org ON policies(org_id);
`
	_, err := pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// v2 migration: task, quality, and ML metrics
	v2 := `
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS tasks_completed INTEGER DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS tasks_started INTEGER DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS avg_task_duration_min REAL DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS stuck_rate REAL DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS phase_distribution JSONB DEFAULT '{}';
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS avg_quality_score SMALLINT DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS quality_degradation_events INTEGER DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS avg_speed_score REAL DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS ml_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS ml_predictions INTEGER DEFAULT 0;
ALTER TABLE daily_metrics ADD COLUMN IF NOT EXISTS ml_retrain_count INTEGER DEFAULT 0;
`
	_, err2 := pool.Exec(ctx, v2)
	if err2 != nil {
		// ALTER TABLE ADD COLUMN IF NOT EXISTS may fail on older PostgreSQL;
		// log but don't fail startup.
		// Actually this is PostgreSQL 9.6+ feature, should be fine.
		return fmt.Errorf("run v2 migrations: %w", err2)
	}

	return nil
}
