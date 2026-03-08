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
