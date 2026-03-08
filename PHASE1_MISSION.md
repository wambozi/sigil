# Phase 1 Mission — Daemon v0

You are working on the `sigild` Go daemon for the Sigil OS project.
Repo: `github.com/wambozi/sigil` (local path is the current working directory).

Your mission: implement every open Phase 1 GitHub issue in the order listed below,
then close each issue via the GitHub API once it passes build and vet.

**GITHUB_TOKEN is set in your environment.** Use it for all API calls.
**Repo slug:** `wambozi/sigil`

## Rules

1. Work through issues in the exact order listed below — later issues depend on earlier ones.
2. Before starting each issue, re-read the relevant source files to understand current state.
3. After implementing each issue:
   - Run `go build ./...` — must pass with zero errors.
   - Run `go vet ./...` — must pass with zero warnings.
   - Run `go test ./...` — all tests must pass (new tests must be added where the acceptance criteria require them).
   - Commit with message: `feat: <short description> (closes #<N>)`
   - Close the GitHub issue via API (see CLAUDE.md for the curl commands).
4. Do not skip an issue. Do not batch commits across issues.
5. Do not introduce import cycles. The dependency graph is in CLAUDE.md.
6. Do not add dependencies beyond `github.com/pelletier/go-toml/v2` (needed for #13).

## Issue Queue (implement in this order)

---

### Issue #22 — Dead code audit: internal/actuator vs internal/notifier

`internal/actuator/actuator.go` predates the notifier package, duplicates notify-send
functionality, and is NOT wired into `cmd/sigild/main.go`.

**Decision:** Delete `internal/actuator/` entirely. The notifier package owns notification.
D-Bus integration is Phase 3 scope. Remove the package and any stale imports.

Acceptance criteria:
- `internal/actuator/` directory deleted
- No remaining imports of `internal/actuator` anywhere
- `go build ./...` and `go vet ./...` pass

---

### Issue #13 — File-based TOML configuration (internal/config package)

Create `internal/config/config.go` that loads `~/.config/sigil/config.toml` and merges
with CLI flag overrides. Flags win over file values.

New dependency: `github.com/pelletier/go-toml/v2`

Config struct must cover all fields in `config.example.toml`:
- `[daemon]` section: log_level, watch_dirs, repo_dirs, db_path, socket_path
- `[notifier]` section: level, digest_time
- `[cactus]` section: url, routing_mode, timeout_seconds
- `[retention]` section: raw_event_days

Acceptance criteria:
- `internal/config/config.go` — `Load(path string) (*Config, error)` function
- `internal/config/config_test.go` — table-driven tests: missing file (returns defaults), partial file (merges), invalid TOML (returns error)
- `cmd/sigild/main.go` wired: load config before flag parsing, flags override
- `go get github.com/pelletier/go-toml/v2` and `go mod tidy`

---

### Issue #7 — Bounded graceful shutdown with drain timeout

`cmd/sigild/main.go` handles SIGINT/SIGTERM via `signal.NotifyContext` but has no
timeout on the shutdown drain. If a collector goroutine hangs, the process never exits.

Acceptance criteria:
- `run()` (or equivalent shutdown sequence in main) creates a fresh context with a
  10-second timeout, passes it to `col.Stop()` and `srv.Stop()`
- If drain exceeds the timeout: log a warning at Error level and `os.Exit(1)`
- Test covers a simulated slow-drain scenario (mock that blocks for > timeout)

---

### Issue #11 — Notification rate limiting

`internal/notifier/notifier.go` — prevent notification floods when the analyzer
produces a burst of suggestions.

Acceptance criteria:
- Notifier tracks `lastShownAt` per level
- `Surface()` skips display (but still stores to DB) if minimum interval has not elapsed
- Constants: `ambientMinInterval = 15 * time.Minute`, `conversationalMinInterval = 5 * time.Minute`
- Table-driven test covers: burst suppression (second call within interval is skipped),
  interval expiry (call after interval succeeds)

---

### Issue #12 — Cactus health check retry before each cloud pass

If Cactus is unreachable at startup and comes up later, the daemon should reconnect
automatically without restarting.

Acceptance criteria:
- `internal/analyzer/analyzer.go`: before each cloud pass, call `cactus.Ping()`;
  if ping fails, skip the cloud pass and log at Warn level; do not cache a nil client
- `internal/cactus/client.go`: ensure `Ping()` is exported and returns a clear error
- No permanent nil/broken client state after a failed startup connection

---

### Issue #10 — Memory budget enforcement via RSS self-monitoring

Daemon self-monitors RSS from `/proc/self/status` and throttles or exits if over budget.

Acceptance criteria:
- Background goroutine reads RSS every 5 minutes
- If RSS > 100MB: log warning, halve all collector polling intervals (best-effort)
- If RSS > 150MB: log error and call `os.Exit(0)` (systemd will restart)
- `sigilctl status` response JSON includes `rss_mb` field
- The `status` socket handler in `cmd/sigild/main.go` must populate `rss_mb`

---

### Issue #15 — Daily digest scheduler

`internal/notifier/notifier.go` has `FlushDigest()` but it is never called.
Level 1 (Digest) suggestions accumulate forever.

Acceptance criteria:
- Background goroutine in `cmd/sigild/main.go` calls `ntf.FlushDigest()` at the
  configured `digest_time` each day (from config, e.g. `"09:00"`)
- Parses `digest_time` string against local time
- Goroutine exits cleanly on context cancellation
- `sigilctl status` includes `next_digest_at` (RFC3339) when notifier level == 1

---

### Issue #16 — macOS notification backend + build tags

Beta testers on macOS need native notifications. `platform_other.go` is currently a no-op.

Acceptance criteria:
- `internal/notifier/platform_darwin.go` — implements `platform` interface via
  `osascript -e 'display notification "..." with title "..."'`
- Build tags on all three files:
  - `platform_linux.go`: `//go:build linux`
  - `platform_darwin.go`: `//go:build darwin`
  - `platform_other.go`: `//go:build !linux && !darwin`
- `go build ./...` must pass (no osascript binary needed at build time — it's exec'd at runtime)

---

### Issue #8 — systemd user service file

Engineers need the daemon to survive reboots and restart on crash.

Acceptance criteria:
- `deploy/sigild.service` — valid systemd user unit:
  ```
  [Unit]
  Description=Sigil daemon
  After=default.target

  [Service]
  Type=simple
  ExecStart=%h/.local/bin/sigild
  Restart=on-failure
  RestartSec=5
  MemoryMax=150M

  [Install]
  WantedBy=default.target
  ```
- File lives at `deploy/sigild.service` in the repo

---

### Issue #9 — sigild init subcommand

One command to bootstrap everything: shell hook, config file, data dir, systemd service.

Acceptance criteria:
- `sigild init` detects `$SHELL` and appends the correct hook source line to
  `~/.zshrc` or `~/.bashrc` (only if not already present)
- Creates `~/.config/sigil/config.toml` from defaults if it does not exist
- Creates `~/.local/share/sigild/` data directory
- On Linux: copies `deploy/sigild.service` to `~/.config/systemd/user/sigild.service`
  and runs `systemctl --user enable --now sigild`
- Prints a confirmation summary of each action taken
- Implement in `cmd/sigild/main.go` as an `init` subcommand (check `os.Args[1]`)

---

### Issue #14 — sigilctl config command

`sigilctl config` is in the CLI help text but not implemented.

Acceptance criteria:
- New socket method `"config"` registered in `cmd/sigild/main.go`; handler returns
  the resolved config as JSON with API key / token fields masked (`***`)
- `cmd/sigilctl/main.go` `config` subcommand: sends `{"method":"config","payload":{}}`
  and prints response in `key = value` format using `text/tabwriter`

---

### Issue #21 — sigilctl purge and export (privacy commands)

Referenced in PRIVACY.md but not yet implemented.

Acceptance criteria:
- `internal/store/store.go`:
  - `Purge() error` — deletes all rows from all tables then removes the SQLite file
  - `Export(w io.Writer) error` — writes all events and suggestions as newline-delimited JSON to w
- `cmd/sigilctl/main.go`:
  - `purge` subcommand: prompts `"This will delete all local data. Type 'yes' to confirm: "`,
    then calls store.Purge() directly (offline, no daemon needed)
  - `export` subcommand: calls store.Export(os.Stdout) directly (offline, no daemon needed)
- Both work without a running daemon (direct DB access)

---

### Issue #18 — GitHub Actions release workflow

Enables binary distribution via GitHub Releases.

Acceptance criteria:
- `.github/workflows/release.yml` triggers on `push` to tags matching `v*`
- Matrix: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
- Each matrix job: `go build` with `-ldflags "-X main.version=${{ github.ref_name }}"`,
  produces binary named `sigild-<os>-<arch>` and `sigilctl-<os>-<arch>`
- SHA256 checksums file generated per platform
- All binaries and checksum files attached to the GitHub Release via `softprops/action-gh-release`

---

### Issue #17 — scripts/install.sh one-line installer

Enables `curl -fsSL .../install.sh | bash` installation.

Acceptance criteria:
- `scripts/install.sh`:
  - Detects OS (`uname -s`) and arch (`uname -m`), maps to release binary names
  - Downloads correct `sigild` and `sigilctl` binaries from latest GitHub Release
  - Verifies SHA256 checksum before installing
  - Installs to `~/.local/bin/`
  - Runs `sigild init` automatically
  - Warns if `~/.local/bin` is not in `$PATH`
  - Works on `linux/amd64` and `linux/arm64`

---

### Issue #19 — PRIVACY.md

Required before beta distribution.

Acceptance criteria, `PRIVACY.md` must cover:
- **What IS collected:** file paths (not contents), command strings, git metadata, process names
- **What is NOT collected:** file contents, keystrokes, screen capture, clipboard
- **Where data lives:** local SQLite only; nothing leaves the machine unless cloud inference is enabled
- **What the LLM receives:** summarized activity counts and pattern labels — never raw file contents or command arguments
- **How to delete all data:** `sigilctl purge`
- **How to audit:** `sigilctl events --all`, `sigilctl export`
- **Cloud inference:** what gets sent to Cactus cloud, how to disable it (set `routing_mode = "local"`)

---

### Issue #20 — README.md

Acceptance criteria, `README.md` must include:
- One-paragraph pitch targeting senior engineers (lead with the self-tuning angle)
- Prominent one-line install command: `curl -fsSL https://raw.githubusercontent.com/wambozi/sigil/main/scripts/install.sh | bash`
- `sigilctl` command reference table (all subcommands with one-line descriptions)
- Link to PRIVACY.md
- Text-based architecture diagram (the event flow from CLAUDE.md)
- Build badge (GitHub Actions) and test badge

---

### Issue #23 — Phase 1 exit criteria validation

Final validation issue. This one is documentation/checklist only — do not write code.

Create `docs/phases/phase_1_daemon_v0.md` that documents:
- All exit criteria and their current status
- How to manually verify each criterion
- Known limitations or deferred items

Exit criteria to document (mark each `[x]` complete or `[ ]` deferred with reason):
- [ ] Daemon runs 48+ hours stable on NixOS (pending NVMe install on 2017 MBP)
- [ ] RSS stays under 50MB during normal operation (verified via `sigilctl status`)
- [ ] Cactus local path: triggered and logged for a low-complexity query
- [ ] Cactus cloud path: triggered and logged for a complex weekly summary
- [ ] All 10+ sigilctl commands return correct responses via socket
- [ ] Shell hook: commands appear in `sigilctl events` within 1 second of execution

After writing the doc, close issue #23.

---

## Completion

When all issues are closed, run:
```bash
curl -s "https://api.github.com/repos/wambozi/sigil/milestones/1" \
  -H "Authorization: token $GITHUB_TOKEN" | grep '"open_issues"'
```

If `open_issues` is 1 (only the epic #2 remains open), close the epic:
```bash
curl -s -X PATCH "https://api.github.com/repos/wambozi/sigil/issues/2" \
  -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github.v3+json" \
  -d '{"state": "closed", "state_reason": "completed"}'
```

Phase 1 is done.
