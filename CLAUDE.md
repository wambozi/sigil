# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Build & Run

All Go commands run from the **repo root** (`/c/Users/nick/workspace/sigil/` on Windows, or the repo root on Linux):

```bash
# Build both binaries
go build ./cmd/sigild/
go build ./cmd/sigilctl/

# Verify no issues
go vet ./...

# Run all tests
go test ./...

# Run the daemon (Linux only — uses Unix sockets, /proc, inotify)
./sigild \
  -watch /home/user/code \
  -repos /home/user/code/myproject \
  -cactus-url http://127.0.0.1:8080 \
  -log-level debug

# Run the CLI
./sigilctl status
./sigilctl events -n 50
./sigilctl tail
./sigilctl events -offline   # read SQLite directly without daemon
```

## Testing

```bash
# Run all tests
go test ./...

# Run a single package's tests
go test ./internal/analyzer/
go test ./internal/socket/
go test -run TestServerHandleStatus ./internal/socket/
```

## Architecture

`sigild` is a Linux daemon that observes developer workflow via four sources, persists everything to a local SQLite database, and surfaces AI-generated insights as desktop notifications.

**Event flow:**

```
Sources → Collector → Store (SQLite)
                  ↓
              Analyzer (timer)
                  ↓ local heuristics + Cactus LLM
              Notifier → notify-send
                  ↑
              Socket server ← sigilctl / shell
```

**Key packages:**

- `internal/event` — shared `Event` and `AIInteraction` types; no internal deps (import-cycle root)
- `internal/store` — SQLite via `modernc.org/sqlite` (pure Go, no CGo); WAL mode
- `internal/collector` — `Source` interface + fan-in to store; `Broadcast` channel for in-process consumers
- `internal/collector/sources` — `FileSource` (fsnotify), `ProcessSource` (/proc polling), `GitSource`, `TerminalSource`
- `internal/cactus` — OpenAI-compatible HTTP client; routing hint sent via `X-Cactus-Routing` header
- `internal/analyzer` — two-tier: local heuristic pass (no network) + periodic cloud pass via Cactus; calls `OnSummary` hook after each cycle
- `internal/notifier` — 5-level suggestion surfacing (0=silent → 4=autonomous); platform-specific backends via `platform` interface
- `internal/socket` — newline-delimited JSON over Unix domain socket; `Handle(method, fn)` registration pattern

**Socket protocol** — requests and responses are newline-delimited JSON:
```json
{"method":"status","payload":{}}
{"ok":true,"payload":{"status":"ok","version":"0.1.0-dev"}}
```
Registered methods: `status`, `events`, `suggestions`, `set-level`, `ingest`, `files`, `commands`, `patterns`, `trigger-summary`, `feedback`

**Cactus routing modes:** `local` | `localfirst` | `remotefirst` | `remote` — daemon starts without Cactus (non-fatal), runs local-only heuristics until it comes up.

**Notifier levels:** 0=Silent, 1=Digest (daily batch), 2=Ambient (default), 3=Conversational, 4=Autonomous

## Platform Notes

- Daemon targets Linux primarily — `platform_linux.go` uses `notify-send`; `platform_darwin.go` uses osascript; `platform_other.go` is a no-op stub
- SQLite DB: `~/.local/share/sigild/data.db` (XDG_DATA_HOME respected)
- Socket: `/run/user/$UID/sigild.sock` (XDG_RUNTIME_DIR respected)
- Raw events pruned after 90 days; `patterns` table kept forever

## Dependency Graph (no cycles allowed)

```
event ← store, collector/sources, collector, analyzer
cactus ← analyzer
socket ← standalone (stdlib only)
cmd/sigild ← everything
```

Adding a new package must not introduce cycles — `event` is the leaf shared by all layers.

## New Dependencies

When adding a dependency, run `go get <module>` then `go mod tidy`. The only approved new dependency for Phase 1 is:
- `github.com/pelletier/go-toml/v2` — for issue #13 (TOML config)

## GitHub Issue Workflow

To close an issue after implementation:
```bash
# Comment + close
ISSUE=<number>
curl -s -X POST "https://api.github.com/repos/wambozi/sigil/issues/${ISSUE}/comments" \
  -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github.v3+json" \
  -d "{\"body\": \"Implemented. See commit $(git rev-parse --short HEAD).\"}"

curl -s -X PATCH "https://api.github.com/repos/wambozi/sigil/issues/${ISSUE}" \
  -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github.v3+json" \
  -d '{"state": "closed", "state_reason": "completed"}'
```

## Phase 1 — Daemon v0: Remaining Issues

These are the open issues in the order they must be implemented (dependencies first).
After each issue: verify `go build ./...` and `go vet ./...` pass, commit, close the issue via API.

### Dependency order

| # | Title | Key dependency |
|---|-------|---------------|
| #22 | Dead code audit: internal/actuator vs internal/notifier | none — decision first |
| #13 | File-based TOML configuration (internal/config) | none — others depend on this |
| #7 | Bounded graceful shutdown with drain timeout | none |
| #11 | Notification rate limiting | none |
| #12 | Cactus health check retry | none |
| #10 | Memory budget enforcement via RSS self-monitoring | status handler |
| #15 | Daily digest scheduler | config (#13) |
| #16 | macOS notification backend + build tags | none |
| #8 | systemd user service file | none |
| #9 | sigild init subcommand | #8, #13 |
| #14 | sigilctl config command | #13 |
| #21 | sigilctl purge and export | store |
| #18 | GitHub Actions release workflow | none |
| #17 | scripts/install.sh one-line installer | #18 |
| #19 | PRIVACY.md | none |
| #20 | README.md | #17, #19 |
| #23 | Phase 1 exit criteria validation | all above |
