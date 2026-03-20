<p align="center">
  <img src="docs/sigil-logo.png" alt="Sigil" width="120" />
</p>

<h1 align="center">Sigil</h1>

<p align="center">
  <strong>OS-level intelligence daemon for software engineers.</strong><br />
  Observes your workflow locally. Surfaces insights. Never sends raw data anywhere.
</p>

<p align="center">
  <a href="https://sigilos.io">Website</a> · <a href="https://github.com/wambozi/sigil/discussions">Discussions</a> · <a href="PRIVACY.md">Privacy</a> · <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center">
  <a href="https://github.com/wambozi/sigil/actions/workflows/release.yml"><img src="https://github.com/wambozi/sigil/actions/workflows/release.yml/badge.svg" alt="Build" /></a>
  <a href="https://github.com/wambozi/sigil/actions/workflows/ci.yml"><img src="https://github.com/wambozi/sigil/actions/workflows/ci.yml/badge.svg" alt="Tests" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0" /></a>
</p>

---

```
sigil@localhost:~/payments $ sigilctl patterns

 PATTERN                    CONFIDENCE   LAST SEEN
 edit-then-test             0.92         2m ago      You run tests within 8s of saving — 94% of the time.
 build-failure-streak       0.87         14m ago     3 consecutive failures on handler/auth.go. Consider isolating the test.
 context-switch-frequency   0.74         6m ago      4 window switches in 90s. Deep work may be fragmenting.
 frequent-files             0.71         1m ago      handler/auth.go edited 11 times today. Possible refactor candidate.
 idle-gap                   0.68         43m ago     22-minute gap after failed build. Break pattern detected.

sigil@localhost:~/payments $ sigilctl suggestions

 ID    CONFIDENCE   STATUS    SUGGESTION
 s-41  0.89         pending   Split handler/auth.go — 3 consecutive test failures suggest isolated concern
 s-40  0.76         pending   Pre-warm payments-db container — you start it manually every morning
 s-39  0.71         accepted  Run go vet before commit — caught 2 issues in last 3 sessions
```

---

Sigil runs as a background daemon (`sigild`) that watches what you're already
doing — file edits, terminal commands, git activity, process state, window
focus — and builds a local model of your habits over time. When it spots a
pattern, it surfaces a suggestion. When you trust the suggestion, it can act
on it.

Everything stays on your machine. The daemon uses pure Go, SQLite in WAL mode,
and zero CGo. It runs in ~15MB RSS and you'll forget it's there until it says
something useful.

> Sigil is the core of [**Sigil OS**](https://sigilos.io), an AI-native Linux
> operating system for engineers. But the daemon works standalone on any Linux
> machine — you don't need Sigil OS to use it.

## Install

### Homebrew (macOS / Linux)

```bash
brew tap wambozi/sigil
brew install sigil
sigild init     # config, shell hooks, launchd (macOS) / systemd (Linux)
```

After init, open a new terminal to activate the shell hook.

### One-line install

```bash
curl -fsSL https://raw.githubusercontent.com/wambozi/sigil/main/scripts/install.sh | bash
```

### Build from source

Requires Go 1.22+.

```bash
git clone https://github.com/wambozi/sigil.git && cd sigil
make install    # builds, installs to $GOPATH/bin, prints next step
sigild init     # config, shell hooks, launchd (macOS) / systemd (Linux)
```

> **Note:** make sure `$GOPATH/bin` (usually `~/go/bin`) is in your `PATH`.

This creates:
- `~/.config/sigil/config.toml` — daemon configuration
- `~/.config/sigil/shell-hook.{zsh,bash}` — terminal integration
- `~/.local/share/sigild/` — data directory
- `~/.config/systemd/user/sigild.service` — auto-start (Linux only)

Open a new shell (or `source ~/.zshrc`) to activate the hook.

## Quick Start

```bash
sigilctl status      # daemon health check
sigilctl tail        # stream live events as you work
sigilctl patterns    # see what the daemon has learned
sigilctl suggestions # view suggestions with confidence scores
```

## Architecture

```
Sources → Collector → Store (SQLite WAL)
                  ↓
              Analyzer (timer) → Detector (15 heuristic checks)
                  ↓               ↓ optional LLM enrichment
              Notifier → notify-send / osascript
                  ↑
              Socket server ← sigilctl / Sigil Shell / VS Code
                  ↑
              Actuator registry (reversible actions)
```

**6 event sources:** filesystem (fsnotify), process monitor (/proc), git
polling, terminal commands (shell hook), Hyprland window focus (IPC), AI
interaction tracking.

**15 pattern detectors** — pure Go heuristics, no LLM required: edit-then-test
correlation, frequent files, build failure streaks, context-switch frequency,
time-of-day productivity, stuck detection, dependency churn, idle gaps, and
more.

**5 notification levels:** Silent → Digest → Ambient (default) →
Conversational → Autonomous. The system earns trust before it acts — suggestions
only surface after the daemon has observed a pattern repeatedly.

**Local inference:** managed `llama-server` lifecycle with 4 routing modes
(`local`, `localfirst`, `remotefirst`, `remote`). Most queries stay on-device.
Cloud models (Anthropic, OpenAI) available as opt-in fallback.

**Reversible actions:** auto-split panes on build, pre-warm containers, dynamic
keybindings — all with an undo window.

**Fleet (enterprise, optional):** anonymized aggregate metrics for team-level
insights. Opt-in per engineer. See [PRIVACY.md](PRIVACY.md).

## sigilctl Commands

| Command | Description |
|---------|-------------|
| `status` | Daemon health, version, RSS |
| `events [-n N]` | Recent events (default 20) |
| `tail` | Stream live events |
| `files` | Top files by edit count (24h) |
| `commands` | Command frequency (24h) |
| `patterns` | Detected patterns + confidence |
| `suggestions` | Suggestion history |
| `summary` | Trigger immediate analysis |
| `level [N]` | Show/set notification level (0–4) |
| `feedback <id> accept\|dismiss` | Respond to a suggestion |
| `config` | Resolved daemon configuration |
| `purge` | Delete all local data |
| `export` | Export as newline-delimited JSON |
| `ai-query <prompt>` | Query the inference engine |
| `fleet-preview` | Preview fleet telemetry payload |
| `fleet-opt-in` / `fleet-opt-out` | Toggle fleet reporting |
| `model list` / `model pull` | Manage inference models |

## Privacy

All data lives in `~/.local/share/sigild/data.db`. Nothing leaves your
machine unless you explicitly configure a cloud inference endpoint and opt in.
Raw events are never transmitted — only aggregated, anonymized metrics if you
enable fleet reporting.

See [PRIVACY.md](PRIVACY.md) for the full data inventory, retention policy,
and opt-out instructions.

## Contributing

Sigil is early. If you're curious, the best way to get involved is to join
[Discussions](https://github.com/wambozi/sigil/discussions) — ask questions,
share ideas, follow along. For code contributions, see
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
