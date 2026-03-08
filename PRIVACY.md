# Privacy Policy

Sigil OS is designed to stay on your machine. This document explains exactly
what data is collected, where it lives, and how to delete it.

---

## What IS collected

Sigil observes your workflow at the metadata level — never the content:

| Signal | What is recorded |
|--------|-----------------|
| **File system** | File paths that were created, modified, or deleted — not file contents |
| **Terminal commands** | The command string and exit code — not stdin/stdout output |
| **Git activity** | Commit counts, branch names, and repository paths — not diffs or commit messages |
| **Process names** | Names of running processes (e.g., `nvim`, `go`, `docker`) — not arguments or environment variables |

All records include a timestamp and are stored in a local SQLite database.

---

## What is NOT collected

- **File contents** — Sigil never reads or stores the text inside your files
- **Keystrokes** — No keystroke logger is present
- **Screen capture** — No screenshots or screen recordings
- **Clipboard** — The clipboard is never accessed
- **Command arguments** — For privacy, only the base command name is recorded by the process monitor (terminal commands capture the full string you typed, but only what your shell sends)
- **Environment variables** — `.env` files and shell environment are never read

---

## Where data lives

All collected data is stored locally in a SQLite database:

```
~/.local/share/sigild/data.db
```

(`$XDG_DATA_HOME/sigild/data.db` if `XDG_DATA_HOME` is set.)

**Nothing leaves your machine unless you explicitly enable cloud inference.**
Raw event data is pruned after 90 days (configurable via `raw_event_days` in
`~/.config/sigil/config.toml`).

---

## What the LLM receives (cloud inference only)

When `mode` is `localfirst`, `remotefirst`, or `remote`, the analyzer
may send a structured summary to the inference engine for deeper reasoning.
The summary contains:

- Aggregated event counts (e.g., "42 file edits, 7 git events in the past hour")
- Pattern labels derived locally (e.g., "frequent context switching detected")
- No raw file paths, no command strings, no file contents

The payload is intentionally coarse to prevent leaking identifying information
even if the cloud backend is hosted remotely.

To disable cloud inference entirely, set `mode = "local"` in your config file
or disable the cloud backend:

```toml
[inference]
mode = "local"

[inference.cloud]
enabled = false
```

---

## How to delete all data

```bash
sigilctl purge
```

This will prompt for confirmation, delete every row from every table, and
remove the SQLite file from disk. The daemon will re-create an empty database
on next startup if it is still running.

---

## How to audit your data

```bash
# List the most recent events
sigilctl events --all

# Export everything as newline-delimited JSON
sigilctl export > my_data.jsonl
```

The export format is human-readable JSON — one record per line — so you can
inspect exactly what has been stored.

---

## Cloud inference — detailed data flow

```
Local events (file paths, cmds, git)
        ↓
  Local analyzer
        ↓
  Aggregated counts + pattern labels   ← only this leaves the machine
        ↓
  Inference engine (local or cloud backend)
        ↓
  LLM narrative → stored locally → shown as notification
```

The inference engine is configured at `~/.config/sigil/config.toml`. By
default both backends are disabled. If you enable the cloud backend, only
the aggregated summary described above is sent.

---

## Questions or concerns

Open an issue at <https://github.com/wambozi/sigil/issues>.
