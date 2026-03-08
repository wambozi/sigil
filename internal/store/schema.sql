-- Sigil daemon local store schema.
-- SQLite in WAL mode — enables concurrent readers alongside the writer.
-- Nothing in this file ever leaves the machine.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Raw observation stream from all collector sources.
CREATE TABLE IF NOT EXISTS events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    kind      TEXT    NOT NULL,          -- event.Kind value
    source    TEXT    NOT NULL,          -- collector source name
    payload   TEXT    NOT NULL,          -- JSON blob
    ts        INTEGER NOT NULL           -- Unix milliseconds
);

CREATE INDEX IF NOT EXISTS idx_events_kind ON events (kind);
CREATE INDEX IF NOT EXISTS idx_events_ts   ON events (ts);

-- AI interaction log.  Separate table so fleet metrics can aggregate
-- without reading raw event payloads.
CREATE TABLE IF NOT EXISTS ai_interactions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    query_text     TEXT,                 -- omitted for privacy-max mode
    query_category TEXT,
    routing        TEXT    NOT NULL,     -- "local" | "cloud"
    latency_ms     INTEGER NOT NULL,
    accepted       INTEGER NOT NULL DEFAULT 0,  -- 0/1
    ts             INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_ts ON ai_interactions (ts);

-- Derived patterns written by the analyzer.
-- Each row is a JSON summary of a detected workflow pattern.
CREATE TABLE IF NOT EXISTS patterns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL,         -- e.g. "file_access_freq", "build_cadence"
    summary    TEXT    NOT NULL,         -- JSON blob
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
