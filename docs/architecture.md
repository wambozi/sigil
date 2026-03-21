# Sigil Platform Architecture

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Engineer's Machine                                │
│                                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │  Editor   │  │ Terminal │  │   Git    │  │  Browser │  │ AI Tools │    │
│  │ (VS Code/ │  │  (zsh/   │  │          │  │          │  │ (Claude/ │    │
│  │  JetBrains)│  │  bash)  │  │          │  │          │  │  Copilot)│    │
│  └─────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘    │
│        │              │              │              │              │          │
│  ┌─────▼──────────────▼──────────────▼──────────────▼──────────────▼─────┐   │
│  │                        sigild (Go daemon)                             │   │
│  │                                                                       │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐  │   │
│  │  │                      Collector (5 sources)                      │  │   │
│  │  │                                                                 │  │   │
│  │  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────┐│  │   │
│  │  │  │  File    │ │ Terminal │ │   Git    │ │ Process  │ │Focus ││  │   │
│  │  │  │ (kqueue/ │ │ (shell  │ │(.git/   │ │(ps/proc) │ │(lsapp││  │   │
│  │  │  │ inotify) │ │  hook)  │ │ fsnotify)│ │          │ │ info)││  │   │
│  │  │  └────┬─────┘ └────┬────┘ └────┬─────┘ └────┬─────┘ └──┬───┘│  │   │
│  │  │       └──────────┬──┴───────────┴────────────┴──────────┘    │  │   │
│  │  └──────────────────┼───────────────────────────────────────────┘  │   │
│  │                     ▼                                              │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │              SQLite Store (WAL mode)                         │  │   │
│  │  │                                                             │  │   │
│  │  │  events │ tasks │ suggestions │ patterns │ ml_predictions  │  │   │
│  │  │  plugin_events │ ml_events │ ml_cursor │ feedback │ actions│  │   │
│  │  │                                                             │  │   │
│  │  │              ~/.local/share/sigild/data.db                  │  │   │
│  │  └──────────┬──────────────┬──────────────┬────────────────────┘  │   │
│  │             │              │              │                        │   │
│  │  ┌──────────▼───┐  ┌──────▼───────┐  ┌──▼──────────────────┐    │   │
│  │  │ Task Tracker │  │   Analyzer   │  │   Notifier          │    │   │
│  │  │              │  │              │  │                      │    │   │
│  │  │ State machine│  │ 21 heuristic │  │ Levels 0-4:         │    │   │
│  │  │ idle→editing │  │ detectors +  │  │ 0=Observer (silent) │    │   │
│  │  │ →verifying   │  │ 6 task-aware │  │ 1=Observer+ (digest)│    │   │
│  │  │ →stuck       │  │ copilot      │  │ 2=Copilot (ambient) │    │   │
│  │  │ →completing  │  │ checks       │  │ 3=Copilot+ (action) │    │   │
│  │  │              │  │              │  │ 4=Autopilot (auto)  │    │   │
│  │  │ Dedup: 6hr   │  │ Hourly cycle │  │                      │    │   │
│  │  └──────────────┘  └──────────────┘  └──────────────────────┘    │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │                    MCP Tool Server                          │  │   │
│  │  │                                                             │  │   │
│  │  │  10 tools backed by store queries:                         │  │   │
│  │  │  get_current_task │ get_task_history │ get_predictions     │  │   │
│  │  │  get_quality_score │ get_suggestions │ get_top_files       │  │   │
│  │  │  get_pr_status │ get_ci_status │ get_recent_commands       │  │   │
│  │  │  get_day_summary                                           │  │   │
│  │  │                         │                                   │  │   │
│  │  │              ┌──────────▼──────────┐                       │  │   │
│  │  │              │   Tool-Calling Loop │                       │  │   │
│  │  │              │                     │                       │  │   │
│  │  │              │ query → LLM → tools │                       │  │   │
│  │  │              │ → results → LLM     │                       │  │   │
│  │  │              │ → answer            │                       │  │   │
│  │  │              └──────────┬──────────┘                       │  │   │
│  │  └─────────────────────────┼───────────────────────────────────┘  │   │
│  │                            │                                      │   │
│  │  ┌─────────────────────────▼───────────────────────────────────┐  │   │
│  │  │                  Inference Engine                            │  │   │
│  │  │                                                             │  │   │
│  │  │  Routing: local │ localfirst │ remotefirst │ remote        │  │   │
│  │  │                                                             │  │   │
│  │  │  ┌─────────────────┐         ┌─────────────────┐          │  │   │
│  │  │  │  Local Backend  │         │  Cloud Backend  │          │  │   │
│  │  │  │  (Ollama/llama) │         │  (Anthropic/    │          │  │   │
│  │  │  │  :11434         │         │   OpenAI)       │          │  │   │
│  │  │  └─────────────────┘         └─────────────────┘          │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │                   Plugin Manager                            │  │   │
│  │  │                                                             │  │   │
│  │  │  HTTP Ingest (:7775) ← plugins POST events                │  │   │
│  │  │                                                             │  │   │
│  │  │  ┌────────────┐  ┌────────────┐  ┌────────────────────┐   │  │   │
│  │  │  │  claude    │  │  github    │  │  47 more in        │   │  │   │
│  │  │  │  (hook)    │  │  (daemon)  │  │  registry (v1-vX)  │   │  │   │
│  │  │  └────────────┘  └────────────┘  └────────────────────┘   │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │                    ML Engine                                │  │   │
│  │  │                                                             │  │   │
│  │  │  Routing: local │ localfirst │ remotefirst │ remote        │  │   │
│  │  │  Predictions persist to ml_predictions table               │  │   │
│  │  │  Retrain trigger after N completed tasks                   │  │   │
│  │  └─────────────────────┬───────────────────────────────────────┘  │   │
│  │                        │                                          │   │
│  │  ┌─────────────────────▼───────────────────────────────────────┐  │   │
│  │  │                   Actuators                                 │  │   │
│  │  │                                                             │  │   │
│  │  │  BuildSplit: split pane on build start                     │  │   │
│  │  │  AutoTest: run tests after file saves (level 4)            │  │   │
│  │  │  All actions reversible with undo windows                  │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  │                                                                   │   │
│  │  Interfaces: Unix socket │ HTTP :7775 (plugins) │ TCP+TLS :7773  │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌───────────────────────────────────────────────────────────────────┐   │
│  │                    sigil-ml (Python sidecar)                      │   │
│  │                                                                   │   │
│  │  FastAPI :7774                                                    │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │                    Event Poller                              │  │   │
│  │  │                                                             │  │   │
│  │  │  Polls events table every 500ms                            │  │   │
│  │  │  Maintains 50-event rolling buffer                         │  │   │
│  │  │  Predicts every 3 new events                               │  │   │
│  │  │  Writes to ml_predictions with TTLs                        │  │   │
│  │  │  Advances ml_cursor atomically                             │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  │                                                                   │   │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌─────────┐ │   │
│  │  │ Stuck        │ │ Suggestion   │ │ Duration     │ │Quality  │ │   │
│  │  │ Predictor    │ │ Policy       │ │ Estimator    │ │Estimator│ │   │
│  │  │              │ │              │ │              │ │         │ │   │
│  │  │ GBT          │ │ Thompson     │ │ GBT          │ │Weighted │ │   │
│  │  │ Classifier   │ │ Sampling     │ │ Regressor    │ │sum, 5   │ │   │
│  │  │              │ │ Bandit       │ │              │ │component│ │   │
│  │  │ "is stuck?"  │ │ "what to     │ │ "how long?"  │ │"quality │ │   │
│  │  │              │ │  say?"       │ │              │ │ 0-100?" │ │   │
│  │  │ TTL: 5min    │ │ TTL: 5min    │ │ TTL: none    │ │TTL:30m  │ │   │
│  │  └──────────────┘ └──────────────┘ └──────────────┘ └─────────┘ │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │              Training Scheduler                             │  │   │
│  │  │                                                             │  │   │
│  │  │  Checks every 10min                                        │  │   │
│  │  │  Retrains after 10 completed tasks (max once/hour)         │  │   │
│  │  │  Hot-reloads models into running poller                    │  │   │
│  │  │  Logs retrain events to ml_events                          │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌───────────────────────────────────────────────────────────────────┐   │
│  │                         CLI (sigilctl)                            │   │
│  │                                                                   │   │
│  │  sigilctl status          sigilctl task          sigilctl ask     │   │
│  │  sigilctl files           sigilctl task history   sigilctl day    │   │
│  │  sigilctl commands        sigilctl suggestions   sigilctl ml      │   │
│  │  sigilctl patterns        sigilctl plugin list   sigilctl level   │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌───────────────────────────────────────────────────────────────────┐   │
│  │                     Ollama (LLM runtime)                          │   │
│  │                                                                   │   │
│  │  :11434    OpenAI-compatible API                                  │   │
│  │            qwen2.5:1.5b / qwen2.5:7b / llama3.1:8b              │   │
│  └───────────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘

                                    │
                                    │ Fleet Reporter (opt-in)
                                    │ Anonymized aggregates only
                                    ▼

┌──────────────────────────────────────────────────────────────────────────┐
│                     Fleet Aggregation Layer (optional)                    │
│                                                                          │
│  ┌──────────────────────┐  ┌──────────────────────────────────────────┐ │
│  │  Fleet Service (Go)  │  │  Fleet Dashboard (Preact + Chart.js)    │ │
│  │                      │  │                                          │ │
│  │  PostgreSQL           │  │  7 views:                               │ │
│  │  :8090               │  │  Adoption │ Velocity │ Cost │ Compliance│ │
│  │                      │  │  Tasks │ Quality │ ML Effectiveness     │ │
│  │  JWT/OIDC auth       │  │                                          │ │
│  │  Policy distribution │  │  ML vs non-ML speed score comparison    │ │
│  └──────────────────────┘  └──────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────┘
```

## Data Flow

```
Engineer works
    │
    ▼
Collectors capture events → SQLite events table
    │
    ├──→ Task Tracker infers phase transitions → tasks table
    │
    ├──→ sigil-ml polls events (500ms) → runs 4 models → ml_predictions table
    │
    ├──→ Plugins push external data → plugin_events table
    │         │
    │         ├── claude: tool calls, AI interactions
    │         └── github: PRs, reviews, comments, CI status
    │
    ├──→ Analyzer runs 21 heuristic checks (hourly) → suggestions
    │
    └──→ Fleet Reporter aggregates metrics (hourly) → fleet service

User asks "what should I work on next?"
    │
    ▼
sigilctl ask → sigild socket → MCP tool loop
    │
    ├── LLM reads: get_current_task, get_predictions, get_pr_status
    │
    ├── MCP tools query: tasks, ml_predictions, plugin_events tables
    │
    └── LLM synthesizes answer grounded in real data

User accepts/dismisses suggestion
    │
    ▼
Feedback → SuggestionPolicy.update() → closes learning loop
```

## Communication Protocols

| Path | Protocol | Port |
|------|----------|------|
| sigilctl ↔ sigild | JSON over Unix socket | `$TMPDIR/sigild.sock` |
| sigild ↔ Ollama | OpenAI-compatible HTTP | :11434 |
| sigild ↔ sigil-ml | HTTP REST | :7774 |
| Plugins → sigild | HTTP POST | :7775 |
| sigild ↔ Fleet | HTTPS POST/GET | :8090 |
| sigild ↔ IDE | TCP+TLS (optional) | :7773 |

## Shared Database Schema

```
                    ┌─────────────────────────────┐
                    │     data.db (SQLite WAL)     │
                    │                              │
   Go-owned         │  events ◄── collectors       │
   (read/write)     │  tasks ◄── task tracker      │
                    │  suggestions ◄── notifier    │
                    │  patterns ◄── analyzer       │
                    │  feedback ◄── user response  │
                    │  action_log ◄── actuators    │
                    │  ai_interactions ◄── LLM     │
                    │  plugin_events ◄── plugins   │
                    │  ml_events ◄── ML audit      │
                    │                              │
   Go creates,      │  ml_predictions              │
   Python writes    │    ◄── sigil-ml poller       │
                    │    ──► MCP tools (read)       │
                    │                              │
   Python-owned     │  ml_cursor                   │
                    │    ◄── sigil-ml poller        │
                    └─────────────────────────────┘
```

## Plugin Ecosystem

```
                    ┌──────────────────────┐
                    │   Plugin Registry    │
                    │   47 plugins         │
                    │                      │
                    │ v1 ─── Core Dev Loop │
                    │   claude (bundled)   │
                    │   github (bundled)   │
                    │   jira              │
                    │   vscode            │
                    │                      │
                    │ v2 ─── Team Workflow │
                    │   linear, confluence │
                    │   notion, slack      │
                    │   gitlab, jetbrains  │
                    │   github-actions     │
                    │   copilot            │
                    │                      │
                    │ v3 ─── Observability │
                    │   sentry, datadog    │
                    │   pagerduty, grafana │
                    │   argocd, k8s        │
                    │                      │
                    │ v4 ─── Communication │
                    │   teams, discord     │
                    │   calendar, figma    │
                    │                      │
                    │ v5 ─── Security      │
                    │   snyk, sonarqube    │
                    │   codecov, semgrep   │
                    │                      │
                    │ vX ─── Extended      │
                    │   30+ more           │
                    └──────────────────────┘
```

## Repos

| Repo | Language | Purpose |
|------|----------|---------|
| [sigil-tech/sigil](https://github.com/sigil-tech/sigil) | Go | Daemon, CLI, plugins, MCP, fleet |
| [sigil-tech/sigil-ml](https://github.com/sigil-tech/sigil-ml) | Python | ML sidecar, 4 models, poller |
| [sigil-tech/homebrew-sigil](https://github.com/sigil-tech/homebrew-sigil) | Ruby | Homebrew formulae |
| [sigil-tech/sigil-os](https://github.com/sigil-tech/sigil-os) | — | Sigil OS (Linux distro) |
