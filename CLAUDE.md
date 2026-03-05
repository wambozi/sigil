# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

All Go commands run from the `daemon/` directory:

```bash
cd daemon

# Build both binaries
go build ./cmd/aetherd/
go build ./cmd/aetherctl/

# Run the daemon (Linux only — uses Unix sockets, /proc, inotify)
./aetherd \
  -watch /home/user/code \
  -repos /home/user/code/myproject \
  -cactus-url http://127.0.0.1:8080 \
  -log-level debug

# Run the CLI
./aetherctl status
./aetherctl events -n 50
./aetherctl tail
./aetherctl events -offline   # read SQLite directly without daemon
```

## Testing

```bash
cd daemon

# Run all tests
go test ./...

# Run a single package's tests
go test ./internal/analyzer/
go test ./internal/socket/
go test -run TestServerHandleStatus ./internal/socket/
```

## Architecture

`aetherd` is a Linux daemon that observes developer workflow via four sources, persists everything to a local SQLite database, and surfaces AI-generated insights as desktop notifications.

**Event flow:**

```
Sources → Collector → Store (SQLite)
                  ↓
              Analyzer (timer)
                  ↓ local heuristics + Cactus LLM
              Notifier → notify-send
                  ↑
              Socket server ← aetherctl / shell
```

**Key packages:**

- `internal/event` — shared `Event` and `AIInteraction` types; no internal deps (import-cycle root)
- `internal/store` — SQLite via `modernc.org/sqlite` (pure Go, no CGo); WAL mode; schema in `internal/store/schema.sql`
- `internal/collector` — `Source` interface + fan-in to store; `Broadcast` channel for in-process consumers
- `internal/collector/sources` — `FileSource` (fsnotify), `ProcessSource` (/proc polling), `GitSource`, `TerminalSource` (receives via socket ingest)
- `internal/cactus` — OpenAI-compatible HTTP client; routing hint sent via `X-Cactus-Routing` header; works with any OpenAI-compatible backend
- `internal/analyzer` — two-tier: local heuristic pass (no network) + periodic cloud pass via Cactus; calls `OnSummary` hook after each cycle
- `internal/notifier` — 5-level suggestion surfacing (0=silent → 4=autonomous); all suggestions persisted to store regardless of level; platform-specific backends via `platform` interface
- `internal/socket` — newline-delimited JSON over Unix domain socket; `Handle(method, fn)` registration pattern

**Socket protocol** — requests and responses are newline-delimited JSON:
```json
{"method":"status","payload":{}}
{"ok":true,"payload":{"status":"ok","version":"0.1.0-dev"}}
```
Registered methods: `status`, `events`, `suggestions`, `set-level`, `ingest`

**Cactus routing modes:** `local` | `localfirst` | `remotefirst` | `remote` — daemon starts without Cactus (non-fatal), runs local-only heuristics until it comes up.

**Notifier levels:** 0=Silent (store only), 1=Digest (daily batch), 2=Ambient (default, moderate confidence threshold), 3=Conversational (action buttons), 4=Autonomous (auto-execute at high confidence)

## Platform Notes

- Daemon targets Linux only — `platform_linux.go` uses `notify-send`; `platform_other.go` is a no-op stub for compilation on other platforms
- SQLite DB: `~/.local/share/aetherd/data.db` (XDG_DATA_HOME respected)
- Socket: `/run/user/$UID/aetherd.sock` (XDG_RUNTIME_DIR respected)
- Raw events pruned after 90 days; `patterns` table kept forever

## Dependency Graph (no cycles allowed)

```
event ← store, collector/sources, collector, analyzer
cactus ← analyzer
analyzer ← actuator (future)
socket ← standalone (stdlib only)
cmd/aetherd ← everything
```

Adding a new package must not introduce cycles — `event` is the leaf shared by all layers.
