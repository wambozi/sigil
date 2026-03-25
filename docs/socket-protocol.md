# Socket Protocol

sigild exposes a JSON-over-Unix-socket protocol at `$XDG_RUNTIME_DIR/sigild.sock`.

## Wire Format

One newline-delimited JSON request per connection, one JSON response back.

```
Request:  {"method":"<name>","payload":{...}}
Response: {"ok":true,"payload":{...}} | {"ok":false,"error":"..."}
```

## Methods

| Method | Payload | Description |
|--------|---------|-------------|
| `shutdown` | none | Graceful daemon shutdown |
| `status` | none | Daemon health check |
| `events` | none | Recent events (last 50) |
| `suggestions` | none | Suggestion history (last 50) |
| `set-level` | `{"level": N}` | Set notification level (0-4) |
| `ingest` | `{"cmd":"...","exit_code":0,"cwd":"...","ts":N,"session_id":"..."}` | Shell hook command ingestion |
| `files` | none | Top edited files (last 24h) |
| `commands` | none | Command frequency table (last 24h) |
| `sessions` | none | Terminal session summaries (last 24h) |
| `patterns` | none | Detected patterns with confidence |
| `trigger-summary` | none | Queue immediate analysis cycle |
| `feedback` | `{"suggestion_id":N,"outcome":"accepted"\|"dismissed"}` | Record suggestion outcome |
| `config` | none | Resolved daemon configuration |
| `ai-query` | `{"query":"...","context":"..."}` | Route query through inference engine |
| `purge` | none | Delete all stored data |
| `undo` | none | Undo most recent reversible action |
| `actions` | none | List recent undoable actions |
| `view-changed` | `{"view":"..."}` | Update keybinding profile |
| `fleet-preview` | none | Preview fleet telemetry payload |
| `fleet-opt-out` | none | Disable fleet reporting |
| `fleet-policy` | none | Current fleet routing policy |

## Push Subscriptions

Clients can subscribe to topics by holding a connection open. Topics:

| Topic | Payload | Description |
|-------|---------|-------------|
| `suggestions` | `{"id":N,"title":"...","text":"...","confidence":0.8,"action_cmd":"..."}` | New suggestion surfaced |
| `actuations` | `{"type":"...","id":"...","description":"...","undo_cmd":"..."}` | Action executed or keybinding profile change |

## `sessions` Response

```json
{
  "ok": true,
  "payload": [
    {
      "session_id": "12345",
      "cmd_count": 42,
      "first_ts": 1741852800,
      "last_ts": 1741856400,
      "last_cwd": "/home/user/project"
    }
  ]
}
```

Sessions without a `session_id` are grouped under `_unknown`.

## `ingest` — Session ID

The `session_id` field is optional. When present (typically the shell PID via `$$`), it enables per-session grouping in the analyzer for more accurate pattern detection. Events from older shell hooks that omit this field continue to work via timestamp-gap fallback.

## Protocol Version

1.2 — Added `shutdown` method for graceful daemon stop via `sigilctl stop`.

1.1 — Added `sessions` method and `session_id` field to `ingest`.
