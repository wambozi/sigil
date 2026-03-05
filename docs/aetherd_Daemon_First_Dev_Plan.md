# aetherd — Daemon-First Development Plan

## Validate the thesis before building the container

**Start date:** Today  
**Target:** Working daemon with local inference, shipping to 10 engineers in 4 weeks  
**Hardware:** Your current Mac (whatever you have), plus an M2 MacBook Air (~$999) if you're on Intel  
**Languages:** Go (daemon), shell scripts (installation)

---

## 1. The Local Model: LFM2-24B-A2B

Your Cactus contact recommended the right model. Here's why it fits and how to run it.

### Why LFM2-24B-A2B Is the Right Choice

LFM2-24B-A2B is a Mixture of Experts model from Liquid AI — 24 billion total parameters, but only 2.3 billion active per token. This MoE architecture means it has the knowledge density of a 24B model with the inference cost of a roughly 2B dense model. The practical impact: it fits in 32GB of RAM and runs at 112 tokens per second on CPU alone. On Apple Silicon with Metal acceleration via Ollama, expect 80-120 tok/s decode speed — fast enough for real-time suggestion generation.

The model supports native function calling (tool use), structured outputs, and a 32K token context window. For the daemon's use case — summarizing developer activity, detecting workflow patterns, generating actionable suggestions — it hits the sweet spot between "smart enough to give useful advice" and "small enough to run on a laptop without the user noticing."

One honest caveat: independent benchmarks score LFM2-24B-A2B at 10 on the Artificial Analysis Intelligence Index, which is below average for models of similar parameter count. Its strengths are speed, efficiency, and general knowledge. Its weaknesses are instruction following and complex reasoning. For the daemon's local tier (pattern detection, simple summaries, command suggestions), this is fine. For the deeper reasoning tier (weekly workflow analysis, complex debugging suggestions, "you seem stuck on X, here are three approaches"), you'll want to call out to a frontier model (Claude via API). This two-tier approach — fast local model for the 80% of queries, frontier cloud model for the 20% — is the right architecture regardless of which local model you choose.

### How to Run It

Ollama is the path of least resistance. It handles model downloading, quantization selection, Metal GPU acceleration, and exposes an OpenAI-compatible API on localhost:11434.

```bash
# Install Ollama
brew install ollama

# Start the service
brew services start ollama

# Pull LFM2-24B-A2B (Q4_K_M quantization — best speed/quality tradeoff)
# Check Ollama's model library for the exact tag — it may be listed as:
ollama pull liquidai/lfm2-24b-a2b

# If not in Ollama's library yet, pull the GGUF from HuggingFace and create a Modelfile:
# 1. Download from: https://huggingface.co/LiquidAI/LFM2-24B-A2B-GGUF
# 2. Create a Modelfile:
#    FROM ./LFM2-24B-A2B-Q4_K_M.gguf
#    PARAMETER temperature 0.3
#    PARAMETER num_ctx 8192
#    SYSTEM "You are aetherd, a developer workflow assistant. You analyze
#    patterns in software development activity and produce concise, actionable
#    suggestions. Be specific. Cite file names, command patterns, and time
#    windows. Never be vague or generic."
# 3. ollama create aetherd-local -f Modelfile

# Verify it runs
ollama run aetherd-local "Summarize: a developer edited 3 Go files in /internal/collector, ran go test 4 times (2 failures), then switched to a browser for 20 minutes. What patterns do you see?"

# Check memory usage while running
# Activity Monitor → Memory → ollama_llama_server
# Should be ~14-16GB for Q4_K_M on Apple Silicon
```

### Hardware Decision

If you're on the 2017 MacBook Pro (Intel, 8-16GB), LFM2-24B-A2B will not run well locally — it needs 14-16GB just for the model weights, leaving nothing for the OS. Two options:

**Option A: Buy an M2 MacBook Air (16GB, ~$999).** This becomes your primary dev machine and reference hardware. LFM2-24B at Q4_K_M will use ~14GB, leaving ~2GB headroom. Tight but workable if you close Chrome. For a more comfortable experience, the 24GB model is ~$1,199.

**Option B: Stay on Intel, use a smaller local model + cloud fallback.** Run Phi-3 Medium (14B, ~8GB at Q4) or Gemma 2B (~1.5GB) locally for the simple stuff, and route complex queries to Claude via API. You lose the "fully local" story but you validate the daemon architecture without new hardware. This is the pragmatic choice if budget is a constraint.

**My recommendation:** Option B to start. The daemon's value proposition doesn't depend on which model runs locally. It depends on whether the observation → analysis → suggestion loop is useful. Validate that with a cheap local model + Claude API, then upgrade hardware when you've proven the thesis.

### Two-Tier Inference Architecture

```
┌─────────────────────────────────────────────────┐
│                  aetherd                         │
│                                                  │
│  Analyzer decides which tier handles each query  │
│                                                  │
│  ┌─────────────────┐  ┌──────────────────────┐  │
│  │   Local Tier     │  │    Cloud Tier         │  │
│  │   (Ollama)       │  │    (Claude API)       │  │
│  │                  │  │                       │  │
│  │  • Pattern match │  │  • Weekly summaries   │  │
│  │  • "You usually  │  │  • "You seem stuck    │  │
│  │    run tests     │  │    on integrating     │  │
│  │    after this"   │  │    X with Y, here     │  │
│  │  • Command       │  │    are 3 approaches"  │  │
│  │    suggestions   │  │  • Code-aware         │  │
│  │  • File relevance│  │    reasoning          │  │
│  │  • Simple Q&A    │  │  • Codebase-level     │  │
│  │                  │  │    insights            │  │
│  │  Latency: <200ms │  │  Latency: 1-5s        │  │
│  │  Cost: $0        │  │  Cost: ~$0.01/query   │  │
│  │  Privacy: total  │  │  Privacy: summarized  │  │
│  └─────────────────┘  └──────────────────────┘  │
└─────────────────────────────────────────────────┘
```

The routing decision is simple: if the query can be answered with pattern matching or a short generation (< 200 tokens), use local. If it requires reasoning over multiple data points or generating a multi-paragraph analysis, use cloud. The daemon makes this decision, not the user.

---

## 2. The Suggestion Architecture

This is the design problem that makes or breaks the product. A suggestion system that's too aggressive is Clippy. One that's too passive is a log file nobody reads. The slider model you described — 0.0 (fully passive) to 1.0 (fully active) — is the right frame, but it needs specifics.

### The Suggestion Spectrum

```
0.0                     0.5                     1.0
 │                       │                       │
 ▼                       ▼                       ▼
PASSIVE                BALANCED                ACTIVE
 │                       │                       │
 Log to file only.       OS notifications        Auto-execute
 Query via CLI.          with actions.            reversible actions.
 No interruptions.       Dismissible.             Undo available.
 │                       │                       │
 "aetherctl show"        Desktop toast:           Terminal auto-runs:
 prints insights         "You run tests after     "go test ./internal/..."
 when YOU ask.           editing collector.go     (with 3s countdown
                         89% of the time.         to cancel)
                         [Run tests] [Dismiss]"
```

### Five Suggestion Levels (Not a Continuous Slider)

A continuous slider sounds elegant but is hard to reason about. Instead, map the 0.0-1.0 range to five discrete levels that users can understand and set:

**Level 0 — Silent (0.0).** All analysis runs. All suggestions are computed. Nothing is surfaced. The user queries insights manually via `aetherctl`. This is for the privacy maximalist who wants the daemon running but doesn't want to be interrupted. Data accumulates; the user pulls when ready.

**Level 1 — Digest (0.25).** A single daily summary delivered at a time the user configures (default: 9am). One notification, one paragraph, covering the previous day's patterns. "Yesterday you spent 62% of your time in the payments service, ran 47 tests (38 passed), and your most-edited file was handler.go. You context-switched between 3 repos an average of every 23 minutes." The user reads it like a morning briefing. No interruptions during the day.

**Level 2 — Ambient (0.5, default).** Real-time suggestions surfaced as non-modal, auto-dismissing desktop notifications. They appear, linger for 8 seconds, then disappear. No sound. No badge. If the user doesn't look at them, they're gone. They accumulate in a notification history queryable via `aetherctl suggestions`. The content is observational: "You've edited 4 files in /internal/collector in the last hour but haven't run tests yet." It never tells you what to do — it tells you what it noticed.

**Level 3 — Conversational (0.75).** Same as Ambient, but suggestions include actionable buttons. The notification has a primary action the user can click to execute. "You usually run `go test ./internal/collector/...` after editing collector.go. [Run now] [Dismiss]." Clicking "Run now" executes the command in the user's terminal. This is the level where the daemon starts *doing things* — but only when the user explicitly clicks.

**Level 4 — Autonomous (1.0).** The daemon takes actions automatically, with a visible countdown and undo. When it detects a pattern match (confidence > 90%), it announces the action ("Running tests in 3s... [Cancel]") and executes unless the user cancels. Post-execution, the action is reversible for 30 seconds. This is the "co-pilot" mode — the daemon is a junior engineer sitting next to you, doing the thing you were about to do anyway.

### Implementation: The Notifier Subsystem

The notifier is a new subsystem in the daemon, sitting between the Analyzer and the user. It decides *how* to surface each suggestion based on the current level.

```go
// internal/notifier/notifier.go

type Level int

const (
    LevelSilent       Level = 0  // 0.0
    LevelDigest       Level = 1  // 0.25
    LevelAmbient      Level = 2  // 0.5
    LevelConversational Level = 3  // 0.75
    LevelAutonomous   Level = 4  // 1.0
)

// Suggestion represents a single insight from the Analyzer.
type Suggestion struct {
    ID         string
    Category   string    // "pattern", "reminder", "optimization", "insight"
    Confidence float64   // 0.0-1.0 — how sure are we this is useful?
    Title      string    // Short headline: "Test after edit?"
    Body       string    // Detail: "You edited collector.go and usually run tests next."
    Action     *Action   // Optional: what clicking "do it" would execute
    CreatedAt  time.Time
    Dismissed  bool
    Accepted   bool
}

// Action represents something the daemon can do.
type Action struct {
    Command     string   // Shell command to execute
    Description string   // Human-readable: "Run go test ./internal/collector/..."
    Reversible  bool     // Can this be undone?
    UndoCommand string   // If reversible, the undo command
}

// Notifier decides how to surface suggestions based on the current level.
type Notifier struct {
    level       Level
    platform    Platform  // macOS, Linux — determines notification mechanism
    history     []Suggestion
    store       *store.DB
}

func (n *Notifier) Surface(s Suggestion) {
    // Always log to store, regardless of level
    n.store.InsertSuggestion(s)
    n.history = append(n.history, s)

    switch {
    case n.level == LevelSilent:
        return // Logged but not shown

    case n.level == LevelDigest:
        n.addToDigest(s) // Accumulated for daily summary

    case n.level == LevelAmbient:
        n.sendNotification(s.Title, s.Body, nil) // No action buttons

    case n.level == LevelConversational:
        if s.Action != nil {
            n.sendNotification(s.Title, s.Body, s.Action) // With action button
        } else {
            n.sendNotification(s.Title, s.Body, nil)
        }

    case n.level == LevelAutonomous:
        if s.Action != nil && s.Confidence > 0.9 {
            n.executeWithCountdown(s) // Auto-execute with cancel window
        } else {
            n.sendNotification(s.Title, s.Body, s.Action)
        }
    }
}
```

### Platform-Specific Notification Delivery

**macOS:** Use `terminal-notifier` (bundled with the daemon) or the `osascript` AppleScript bridge. Both produce native macOS Notification Center toasts. For action buttons, `terminal-notifier` supports `-actions` for clickable buttons that can trigger a callback URL or command. The Go library `gen2brain/beeep` provides a clean cross-platform wrapper.

**Linux:** Use `notify-send` (D-Bus notifications) on GNOME/KDE desktops, or `dunstify` for tiling WM users. Action buttons are supported via D-Bus notification spec.

**Terminal-only fallback:** For users running in a headless/SSH environment, the daemon writes suggestions to a FIFO pipe that the user's shell integration script reads and displays inline (like a `PROMPT_COMMAND` hook that checks for pending suggestions).

### The Confidence System

Not all suggestions are equal. The daemon tracks confidence based on pattern strength:

```go
// Confidence thresholds
const (
    ConfidenceWeak     = 0.3  // Seen 2-3 times. Don't surface below Level 3.
    ConfidenceModerate = 0.6  // Seen 5-10 times. Surface at Level 2+.
    ConfidenceStrong   = 0.8  // Seen 15+ times. Surface at Level 1+.
    ConfidenceVeryStrong = 0.9 // Seen 25+ times. Eligible for auto-execute.
)
```

At Level 2 (Ambient), the daemon only surfaces suggestions with confidence ≥ 0.6. This means the user never sees a suggestion until the daemon has observed the pattern at least 5-10 times. This is critical for trust — early suggestions should feel eerily accurate, not noisy. The daemon earns the right to be louder over time.

### The Feedback Loop

Every suggestion tracks its outcome: dismissed, accepted, or ignored (auto-dismissed after timeout). This feeds back into the analyzer:

- **Accepted 3+ times:** Boost confidence for this pattern. Consider promoting to auto-execute at Level 4.
- **Dismissed 3+ times:** Suppress this suggestion permanently. The daemon learns what the user doesn't want.
- **Ignored consistently:** Reduce frequency. Maybe this is a valid pattern but the user doesn't need to be told.

This means the system gets quieter and more accurate over time. The first week is the noisiest. By week 3, only the suggestions that the user actually acts on survive.

---

## 3. The Daemon Architecture (Revised for Standalone)

### What Changed from the OS Plan

The daemon no longer assumes it's running inside Aether OS. It runs on any macOS or Linux machine as a standalone Go binary. The Hyprland IPC collector is gone. The shell event bus is gone. Instead, the daemon uses universally available observation sources:

```
┌──────────────────────────────────────────────────┐
│                    aetherd                        │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │              Collector                     │   │
│  │                                            │   │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────┐ │   │
│  │  │ fsnotify │ │   git    │ │  terminal  │ │   │
│  │  │          │ │          │ │            │ │   │
│  │  │ Watches  │ │ Polls    │ │ Shell      │ │   │
│  │  │ project  │ │ .git/    │ │ history    │ │   │
│  │  │ dirs for │ │ for      │ │ integration│ │   │
│  │  │ file     │ │ commits, │ │ (zsh/bash  │ │   │
│  │  │ changes  │ │ branches,│ │ hooks)     │ │   │
│  │  │          │ │ diffs    │ │            │ │   │
│  │  └──────────┘ └──────────┘ └────────────┘ │   │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────┐ │   │
│  │  │  proc    │ │  build   │ │    ai      │ │   │
│  │  │          │ │          │ │            │ │   │
│  │  │ Active   │ │ Watches  │ │ Tracks     │ │   │
│  │  │ process  │ │ for make,│ │ daemon's   │ │   │
│  │  │ list,    │ │ go build,│ │ own LLM    │ │   │
│  │  │ resource │ │ npm test │ │ queries,   │ │   │
│  │  │ usage    │ │ output & │ │ routing,   │ │   │
│  │  │          │ │ exit     │ │ acceptance │ │   │
│  │  │          │ │ codes    │ │ rates      │ │   │
│  │  └──────────┘ └──────────┘ └────────────┘ │   │
│  └───────────────────────────────────────────┘   │
│                      │                            │
│                      ▼                            │
│  ┌───────────────────────────────────────────┐   │
│  │            SQLite Store                    │   │
│  │  events table + suggestions table          │   │
│  │  + feedback table                          │   │
│  └───────────────────────────────────────────┘   │
│                      │                            │
│                      ▼                            │
│  ┌───────────────────────────────────────────┐   │
│  │              Analyzer                      │   │
│  │                                            │   │
│  │  Local: frequency tables, pattern detect   │   │
│  │  LLM:   Ollama (local) or Claude (cloud)   │   │
│  └───────────────────────────────────────────┘   │
│                      │                            │
│                      ▼                            │
│  ┌───────────────────────────────────────────┐   │
│  │              Notifier                      │   │
│  │                                            │   │
│  │  Level 0-4 suggestion surfacing            │   │
│  │  Platform-native notifications             │   │
│  │  Feedback tracking                         │   │
│  └───────────────────────────────────────────┘   │
│                      │                            │
│                      ▼                            │
│  ┌───────────────────────────────────────────┐   │
│  │           Unix Socket Server               │   │
│  │                                            │   │
│  │  aetherctl queries, future shell conn      │   │
│  └───────────────────────────────────────────┘   │
└──────────────────────────────────────────────────┘
```

### Shell Integration (How Terminal Commands Get Captured)

The daemon needs to know what commands the user runs. On macOS/Linux, the cleanest approach is a shell hook — a small function added to the user's `.zshrc` or `.bashrc` that notifies the daemon after each command:

```bash
# Added to ~/.zshrc during `aetherd init`
aetherd_precmd() {
    local exit_code=$?
    local cmd=$(fc -ln -1 | sed 's/^[[:space:]]*//')
    # Send to daemon via Unix socket (non-blocking, fire-and-forget)
    echo "{\"kind\":\"command\",\"cmd\":\"$cmd\",\"exit_code\":$exit_code,\"cwd\":\"$PWD\",\"ts\":$(date +%s)}" | \
        nc -U -w0 ~/.local/share/aether/aetherd.sock 2>/dev/null &
}
precmd_functions+=(aetherd_precmd)
```

This is invisible to the user. It adds <1ms latency per command. The daemon receives a structured event for every command executed, including the exit code (so it knows about build/test failures) and the working directory (so it knows context).

### Build/Test Detection

The daemon watches for known build/test commands and tracks their outcomes:

```go
// internal/collector/build.go

var buildPatterns = []struct {
    Pattern  *regexp.Regexp
    Language string
    Kind     string // "build", "test", "lint", "format"
}{
    {regexp.MustCompile(`^go (test|build|run|vet)`), "go", ""},
    {regexp.MustCompile(`^npm (test|run test|run build)`), "node", ""},
    {regexp.MustCompile(`^npx jest`), "node", "test"},
    {regexp.MustCompile(`^make\b`), "make", "build"},
    {regexp.MustCompile(`^cargo (test|build|run|clippy)`), "rust", ""},
    {regexp.MustCompile(`^pytest`), "python", "test"},
    {regexp.MustCompile(`^docker (build|compose)`), "docker", "build"},
    {regexp.MustCompile(`^golangci-lint`), "go", "lint"},
    {regexp.MustCompile(`^tsc\b`), "node", "build"},
}
```

---

## 4. Week-by-Week Execution Plan

### Week 1: Core Loop (Days 1-7)

The goal is a daemon that watches files, records commands, and talks to a local model. No UI, no notifications, just the data pipeline.

**Day 1-2: Project scaffold and store.**

```bash
mkdir -p ~/code/aetherd
cd ~/code/aetherd
go mod init github.com/yourusername/aetherd
```

Build these files:

- `cmd/aetherd/main.go` — Main entry point. Initializes store, starts collectors, blocks on signal.
- `cmd/aetherctl/main.go` — CLI client. Connects to Unix socket.
- `internal/store/store.go` — SQLite wrapper. Three tables: `events`, `suggestions`, `feedback`. Write `InsertEvent`, `QueryEvents`, `InsertSuggestion`, `QuerySuggestions`, `InsertFeedback` methods.
- `internal/store/store_test.go` — Tests for all store methods.

The events schema:

```sql
CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    source     TEXT NOT NULL,   -- 'fs', 'git', 'terminal', 'proc', 'build', 'ai'
    kind       TEXT NOT NULL,   -- 'file_modify', 'command', 'commit', 'test_fail', etc.
    context    TEXT,            -- JSON: {"path":"/internal/collector/collector.go"}
    metadata   TEXT             -- JSON: {"exit_code":0, "duration_ms": 1200}
);

CREATE TABLE suggestions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    category    TEXT NOT NULL,  -- 'pattern', 'reminder', 'optimization', 'insight'
    confidence  REAL NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    action_cmd  TEXT,           -- Shell command if actionable
    status      TEXT NOT NULL DEFAULT 'pending', -- 'pending','shown','accepted','dismissed','ignored'
    shown_at    TEXT,
    resolved_at TEXT
);

CREATE TABLE feedback (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    suggestion_id INTEGER NOT NULL REFERENCES suggestions(id),
    outcome       TEXT NOT NULL,  -- 'accepted', 'dismissed', 'ignored'
    timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
```

Exit criteria: `go test ./internal/store/...` passes. You can insert and query events.

**Day 3: File system collector.**

- `internal/collector/fs.go` — Uses `github.com/fsnotify/fsnotify`. Watches configured project directories (default: `~/code`, configurable). Logs `file_create`, `file_modify`, `file_delete`, `file_rename` events with the full path and file extension. Debounces rapid-fire events (IDEs save multiple times per keystroke) — batch events within a 500ms window.
- Ignore patterns: `.git/`, `node_modules/`, `vendor/`, `__pycache__/`, build artifacts.

Exit criteria: Edit a file in your project directory. `aetherctl events --source fs --last 1h` shows the event.

**Day 4: Terminal command collector.**

- `internal/collector/terminal.go` — Listens on the Unix socket for command events from the shell hook.
- `internal/ipc/server.go` — Unix socket server at `~/.local/share/aether/aetherd.sock`. Accepts newline-delimited JSON messages. Routes command events to the terminal collector.
- Shell integration script: `scripts/shell-hook.zsh` and `scripts/shell-hook.bash`. The `aetherd init` command appends the hook to the user's shell config.

Exit criteria: Run commands in your terminal. `aetherctl events --source terminal --last 1h` shows them with exit codes and working directories.

**Day 5: Git collector.**

- `internal/collector/git.go` — For each watched project directory, polls `.git/` for changes every 30 seconds. Detects new commits (by comparing HEAD), branch switches (reading `.git/HEAD`), and modified file counts (parsing `git status --porcelain`). Doesn't shell out to `git` for every poll — reads the git internals directly where possible, falls back to `git` commands where necessary.

Exit criteria: Make a commit. `aetherctl events --source git --last 1h` shows the commit event with branch name, message preview, and files changed count.

**Day 6-7: Local model integration.**

- `internal/inference/client.go` — HTTP client for Ollama's OpenAI-compatible API at `localhost:11434`. Methods: `Complete(ctx, systemPrompt, userPrompt string) (string, error)` and `CompleteWithTools(ctx, systemPrompt, userPrompt string, tools []Tool) (string, error)`.
- `internal/inference/client.go` also has a `CloudClient` for Claude API (`ANTHROPIC_API_KEY` env var). Same interface.
- `internal/inference/router.go` — Decides local vs. cloud based on query complexity. For now, simple heuristic: if the prompt is under 500 tokens and the query category is "pattern" or "command", use local. Otherwise, use cloud.
- Wire it up: the daemon runs a summary every 4 hours (configurable). It queries the last 4 hours of events from the store, constructs a prompt, sends it to the local model, and logs the response as an event of source "ai".

The summary prompt template:

```
You are aetherd, a developer workflow assistant. Analyze the following
activity log and produce 2-3 specific, actionable observations. Be concise.
Reference specific file paths, command patterns, and time windows.

ACTIVITY (last 4 hours):
- Files modified: {{.FilesSummary}}
- Commands run: {{.CommandsSummary}}  
- Build/test results: {{.BuildSummary}}
- Git activity: {{.GitSummary}}

Respond with observations in this JSON format:
[
  {
    "category": "pattern|reminder|optimization|insight",
    "confidence": 0.0-1.0,
    "title": "Short headline (under 60 chars)",
    "body": "One sentence detail.",
    "action_cmd": "optional shell command or null"
  }
]
```

Exit criteria: The daemon runs, collects events for 4 hours, summarizes them via Ollama, and the summary is stored in the database. `aetherctl summary` prints the latest summary.

### Week 2: Notifier and CLI Polish (Days 8-14)

**Day 8-9: Notifier subsystem.**

- `internal/notifier/notifier.go` — The five-level system described in Section 2.
- `internal/notifier/platform_darwin.go` — macOS notifications via `terminal-notifier` or `osascript`.
- `internal/notifier/platform_linux.go` — Linux notifications via `notify-send`.
- Configuration: notification level is stored in `~/.config/aether/config.toml`.

```toml
# ~/.config/aether/config.toml
[notifier]
level = 2  # 0=silent, 1=digest, 2=ambient, 3=conversational, 4=autonomous

[collector]
watch_dirs = ["~/code"]
ignore_patterns = [".git", "node_modules", "vendor"]

[inference]
local_endpoint = "http://localhost:11434"
local_model = "aetherd-local"
cloud_provider = "anthropic"  # or "openai"
# ANTHROPIC_API_KEY read from env

[schedule]
summary_interval = "4h"
digest_time = "09:00"
```

Exit criteria: Set level to 2. Edit some files and run some commands. After 4 hours, a macOS notification appears with the summary. Dismiss it. Check `aetherctl suggestions` — it shows the suggestion with status "dismissed".

**Day 10-11: Pattern detection (local analyzer).**

- `internal/analyzer/patterns.go` — Pure Go, no ML. Detects:
  - **Edit-then-test:** If the user edits a file in directory X and then runs a test command within 5 minutes, more than 60% of the time, generate a "you usually test after editing X" suggestion.
  - **Frequent files:** Top 5 most-edited files per day. Surface if a file suddenly appears in the top 5 that wasn't there yesterday ("you're spending more time in handler.go than usual — new feature or bug?").
  - **Build failure streaks:** If 3+ consecutive build/test commands fail, suggest "you've had 3 failures in a row — want a summary of the errors?"
  - **Context switch frequency:** If the user changes working directories more than 6 times per hour, note it ("high context-switching today — 8 directory changes in the last hour").
  - **Time-of-day patterns:** Track productive hours (most file edits and commits). Surface in the daily digest.

Each pattern has a confidence score that increases with observation count.

Exit criteria: Work normally for a day. The next morning's digest (Level 1) or ambient notifications (Level 2) include pattern-based observations, not just raw LLM summaries.

**Day 12-13: aetherctl polish.**

Commands to implement:

```bash
aetherctl status          # Daemon uptime, event count, memory, model status
aetherctl events          # Recent events (filterable by --source, --kind, --last)
aetherctl files           # Top files by edit count today
aetherctl commands        # Command frequency table today
aetherctl patterns        # Detected patterns with confidence scores
aetherctl suggestions     # Suggestion history with status
aetherctl summary         # Trigger an immediate summary (bypass interval)
aetherctl level           # Show current notification level
aetherctl level 3         # Set notification level
aetherctl config          # Print current configuration
aetherctl feedback <id> accept|dismiss  # Manually respond to a suggestion
```

All commands talk to the daemon via the Unix socket. Output is clean, tabular, colored for terminal.

Exit criteria: Every command above works and produces useful output.

**Day 14: Integration test and dog-fooding.**

- Use the daemon yourself for a full day of real development work.
- Fix bugs you encounter.
- Note: which suggestions were useful? Which were noise? What patterns did it miss?
- Write down 5 things you'd tell a friend about the experience.

### Week 3: Hardening and Distribution (Days 15-21)

**Day 15-16: Reliability.**

- Graceful shutdown (flush SQLite WAL, close socket).
- Crash recovery (daemon restarts via launchd/systemd without losing data).
- Memory budget enforcement (if RSS > 100MB, log warning and reduce collector polling frequency).
- Ollama health check (if Ollama isn't running, fall back to cloud-only mode silently).
- Rate limiting on notifications (never more than 1 notification per 15 minutes at Level 2, 1 per 5 minutes at Level 3).

**Day 17-18: Installation and onboarding.**

Create a one-line installer:

```bash
curl -fsSL https://raw.githubusercontent.com/yourusername/aetherd/main/install.sh | bash
```

The install script:
1. Downloads the pre-built binary for the current OS/arch from GitHub Releases
2. Copies to `~/.local/bin/aetherd` and `~/.local/bin/aetherctl`
3. Creates `~/.config/aether/config.toml` with defaults
4. Creates the data directory at `~/.local/share/aether/`
5. Installs the launchd plist (macOS) or systemd user service (Linux)
6. Appends the shell hook to `~/.zshrc` (with a `# Added by aetherd` comment)
7. Starts the daemon
8. Prints: "aetherd is running. It will observe your workflow and surface suggestions. Run `aetherctl status` to verify. Run `aetherctl level 0` to go silent."

Build a GitHub Actions workflow that cross-compiles for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` and attaches binaries to GitHub Releases.

**Day 19-20: Privacy and trust documentation.**

Write a `PRIVACY.md` in the repo:
- What data is collected (file paths, command strings, git metadata, process names)
- What data is NOT collected (file contents, keystroke logging, screen capture, clipboard)
- Where data is stored (local SQLite, never leaves the machine unless cloud inference is enabled)
- What goes to the LLM (summarized activity — file counts, command patterns, error messages — never raw file contents)
- How to delete all data (`aetherctl purge`)
- How to see exactly what the daemon knows (`aetherctl events --all`, `aetherctl export`)

**Day 21: Prepare for beta.**

- Write a 1-paragraph pitch (see GTM section)
- Record a 2-minute terminal recording (asciinema) showing: install, work for a few minutes, get a notification, interact with `aetherctl`
- Create a simple landing page (GitHub README with the pitch, recording, install command, and PRIVACY.md link)

### Week 4: Beta Distribution (Days 22-28)

**Day 22-23: Recruit 10 testers.**

Reach out to 10 senior engineers in your network. The message:

> "I built a daemon that watches how you develop — files, git, terminal, build patterns — and surfaces suggestions when it notices patterns. It caught that I was manually restarting a service after every test and suggested an alias. It noticed I context-switch between repos every 23 minutes and asked if they should be in the same workspace. Runs locally, all data on your machine, single Go binary. Takes 30 seconds to install. Want to try it for a week?"

Target: people who have opinions about their dev environment. Staff/senior engineers. People with dotfiles repos. People who use tiling WMs or custom terminal setups. They're the psychographic that will care about this.

**Day 24-25: Telemetry for the beta (opt-in).**

Add a minimal, opt-in anonymous telemetry endpoint so you can measure:
- Daily active daemons (heartbeat)
- Notification level distribution (what level do people settle on?)
- Suggestion acceptance rate (by category)
- Retention (is the daemon still running after 1 day? 3 days? 7 days?)

This is not the enterprise fleet layer. This is standard beta telemetry. Make it opt-in with a clear prompt during install. If the user says no, zero data leaves their machine.

**Day 26-28: Gather feedback, iterate.**

- Check in with testers after 3 days and 7 days
- Key questions: "Is it still running? Have you changed the notification level? Which suggestion was most useful? Which was most annoying? Would you keep using it?"
- 7-day retention > 50% = the thesis is validated
- Suggestion acceptance rate > 30% = the content is useful
- Most users settle on Level 2-3 = the notification architecture works

---

## 5. Project Structure (Final)

```
aetherd/
├── cmd/
│   ├── aetherd/
│   │   └── main.go              # Daemon entry point
│   └── aetherctl/
│       └── main.go              # CLI client
├── internal/
│   ├── collector/
│   │   ├── collector.go         # Collector manager (starts/stops all sources)
│   │   ├── fs.go                # File system watcher (fsnotify)
│   │   ├── git.go               # Git activity poller
│   │   ├── terminal.go          # Terminal command receiver (from shell hook)
│   │   ├── proc.go              # Process monitor (/proc or ps)
│   │   └── build.go             # Build/test pattern matcher
│   ├── analyzer/
│   │   ├── analyzer.go          # Analyzer manager
│   │   ├── patterns.go          # Local pattern detection (frequency tables)
│   │   └── summarizer.go        # LLM summary generation
│   ├── inference/
│   │   ├── client.go            # Ollama client (OpenAI-compatible API)
│   │   ├── cloud.go             # Claude API client
│   │   └── router.go            # Local vs. cloud routing
│   ├── notifier/
│   │   ├── notifier.go          # Level-based suggestion surfacing
│   │   ├── platform_darwin.go   # macOS notifications
│   │   └── platform_linux.go    # Linux notifications
│   ├── store/
│   │   ├── store.go             # SQLite wrapper
│   │   └── store_test.go        # Store tests
│   ├── ipc/
│   │   ├── server.go            # Unix socket server
│   │   └── protocol.go          # JSON message types
│   └── config/
│       └── config.go            # TOML config loader
├── scripts/
│   ├── shell-hook.zsh           # Zsh integration hook
│   ├── shell-hook.bash          # Bash integration hook
│   └── install.sh               # One-line installer
├── .github/
│   └── workflows/
│       └── release.yml          # Cross-compile and publish
├── go.mod
├── go.sum
├── config.example.toml
├── PRIVACY.md
├── README.md
└── LICENSE                      # Apache 2.0
```

---

## 6. Key Dependencies

```
github.com/fsnotify/fsnotify     # File system events
github.com/mattn/go-sqlite3      # SQLite driver (requires CGO)
github.com/gen2brain/beeep       # Cross-platform notifications
github.com/pelletier/go-toml/v2  # Config file parsing
github.com/fatih/color           # Colored terminal output for aetherctl
github.com/olekukonko/tablewriter # Table formatting for aetherctl
```

No web frameworks. No ORMs. No dependency injection frameworks. The daemon is a simple Go binary with a tight dependency surface.

---

## 7. Success Criteria

After 4 weeks, you should be able to answer these questions:

**Is the observation pipeline working?**
- The daemon runs for 7+ days without crashing
- It captures 90%+ of file edits, terminal commands, and git activity
- Memory stays under 100MB

**Is the local model useful for pattern detection?**
- LFM2-24B-A2B (or your fallback model) generates suggestions that reference real file paths and commands
- At least 1 in 3 suggestions is something the user would actually act on
- The local model responds in under 2 seconds

**Is the notification architecture right?**
- Most users settle on Level 2 or 3 (not 0 or 4)
- Suggestion acceptance rate > 30%
- No user complains about notification fatigue after the first day

**Do people keep using it?**
- 7-day retention > 50% across 10 testers
- At least 3 testers say "I'd miss this if you took it away"
- At least 1 tester asks "when can I get this for my team?"

If you hit these numbers, the thesis is validated and you have the signal to build the shell (and eventually the OS) on top. If you don't, you've spent 4 weeks and learned exactly why — and you can iterate on the daemon without having wasted months on infrastructure.

---

## 8. What Comes After (If It Works)

**Month 2:** Build a lightweight TUI (terminal UI) companion using Bubble Tea or similar. This becomes the "Aether Shell v0" — a terminal-based dashboard showing live suggestions, event stream, pattern confidence, and the notification level slider. It runs alongside your normal terminal, not replacing it.

**Month 3:** If the TUI proves the integrated-view concept, graduate to the Tauri shell. Now you've validated the daemon, validated the suggestion architecture, validated the display model, and *then* you build the full GUI shell.

**Month 4+:** OS packaging. By this point you know what the daemon needs from the OS (deeper instrumentation? Compositor access? Container management?) and can make informed decisions about NixOS, Hyprland, cage, and the VM distribution model.

The OS is the endgame. The daemon is where you start.
