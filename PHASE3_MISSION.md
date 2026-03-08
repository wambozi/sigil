# Phase 3 Mission — Intelligence, Actuator & Polish

The daemon gets smart, the shell gets refined, and the feedback loop closes.

**Repos involved:**
- `wambozi/sigil` — Go daemon (`sigild`, `sigilctl`); local path `../sigil/` or `/home/nick/workspace/sigil/`
- `wambozi/sigil-os` — Tauri shell + NixOS config; local path `../sigil-os/` or `/home/nick/workspace/sigil-os/`

**All GitHub issues live on `wambozi/sigil`.** Shell/OS work that happens in `wambozi/sigil-os` still closes issues on `wambozi/sigil`.

---

## Rules

1. Work through issues in the exact order listed below — dependencies are front-loaded.
2. Before starting each issue, read every file listed under "Read first."
3. After completing each issue:
   - Run the build verification commands listed for that issue.
   - Commit with message: `feat: <short description> (closes #<N>)`
   - Close the GitHub issue via `gh` CLI (commands below).
4. Never skip an issue. Never batch commits across issues.
5. The actuator infrastructure step (between #105 and #106) has no GitHub issue. Commit it under the message: `feat: actuator package infrastructure (prereq for #106-#108)` — no issue to close.
6. Close duplicate issues 38–50 before starting any implementation (see Duplicate Cleanup below).

---

## Build & Verify Commands

### Daemon (`/home/nick/workspace/sigil/`)
```bash
go build ./...
go vet ./...
go test ./...
```

### Shell frontend (`/home/nick/workspace/sigil-os/shell/`)
```bash
npm run build
```
(TypeScript must compile with zero type errors.)

### Shell Rust backend (`/home/nick/workspace/sigil-os/shell/src-tauri/`)
```bash
cargo build
```

---

## GitHub Issue Workflow

After each successful build + commit, close the issue:

```bash
# Close with gh CLI (preferred)
gh issue close <NUMBER> --repo wambozi/sigil --comment "Implemented. See commit $(git -C /home/nick/workspace/sigil rev-parse --short HEAD 2>/dev/null || git -C /home/nick/workspace/sigil-os rev-parse --short HEAD)."

# If closing from sigil-os repo, reference commit from that repo:
gh issue close <NUMBER> --repo wambozi/sigil --comment "Implemented in wambozi/sigil-os. See commit $(git -C /home/nick/workspace/sigil-os rev-parse --short HEAD)."
```

---

## Duplicate Cleanup

Issues 38–50 are duplicates of 103–115 (same titles, same work). Close them as duplicates **before starting any implementation**:

```bash
for N in 38 39 40 41 42 43 44 45 46 47 48 49 50; do
  gh issue close $N --repo wambozi/sigil --comment "Duplicate of the corresponding issue in the 103–115 range. Closing as duplicate."
done
```

---

## Issue Queue

Implement issues in this exact order.

---

### Issue #103 — Enhanced local heuristic model (temporal analysis, AI interaction patterns)

**Repo:** `wambozi/sigil`

**Read first:**
- `internal/analyzer/patterns.go` — existing 5 detectors; understand the `Detector.Detect()` pattern
- `internal/store/store.go` — `QueryTerminalEvents`, `QueryRecentFileEvents`, `QuerySuggestions`
- `internal/event/event.go` — `AIInteraction` type

**Acceptance criteria (from issue):**
- Temporal patterns: time-of-day productivity windows, day-of-week patterns, session length distribution
- AI interaction patterns: which query categories are most used, what time AI mode is most active, suggestion acceptance trends over time
- Pattern confidence scores improve as more data accumulates
- All new detectors follow the existing `Detector.Detect()` pattern

**What to build:**

*`internal/store/store.go`* — add two new query methods:

```go
// QueryAIInteractions returns AI interaction records since the given time, ordered ascending.
func (s *Store) QueryAIInteractions(ctx context.Context, since time.Time) ([]event.AIInteraction, error)

// QuerySuggestionAcceptanceRate returns the ratio of accepted/(accepted+dismissed) suggestions
// since the given time. Returns 0.0 if no resolved suggestions exist.
func (s *Store) QuerySuggestionAcceptanceRate(ctx context.Context, since time.Time) (float64, error)
```

`QueryAIInteractions` queries `ai_interactions` WHERE `ts >= since` ORDER BY `ts ASC`, scanning all columns.

`QuerySuggestionAcceptanceRate` queries COUNT(*) for status='accepted' and COUNT(*) for status IN ('accepted','dismissed') from `suggestions` WHERE `resolved_at >= since`. Return accepted/total (0.0 if total=0).

*`internal/analyzer/patterns.go`* — add four new check functions to `Detector`, and register them in the `checks` slice inside `Detect()`:

1. **`checkDayOfWeekProductivity`** — groups file-edit counts by `e.Timestamp.Weekday()`, finds the most productive day. Emits a suggestion when the difference between peak and trough days is >= 2x and there are >= 10 edits on the peak day. Category: `"insight"`, Confidence: `ConfidenceWeak`.

2. **`checkSessionLength`** — scans terminal events ordered by time; defines a "session gap" as any gap > 2 hours between consecutive events. Computes average session length in minutes across the window. Emits if avg > 60 minutes and data spans >= 3 sessions. Body: `"Average coding session: %d minutes (based on terminal activity)."` Category: `"insight"`, Confidence: `ConfidenceWeak`.

3. **`checkAIQueryCategoryTrends`** — calls `store.QueryAIInteractions` for the window. Tallies interactions by `QueryCategory`. Emits a suggestion for the top category if it accounts for >= 50% of queries and there are >= 5 total. Body: `"Most of your AI queries are about %s (%d%% of recent queries)."` Category: `"insight"`, Confidence: `ConfidenceModerate`.

4. **`checkSuggestionAcceptanceTrend`** — calls `store.QuerySuggestionAcceptanceRate`. If rate >= 0.7, emits a positive reinforcement insight. If rate < 0.3 and there are >= 10 resolved suggestions (query count separately), emits a suggestion to adjust the notification level. Category: `"insight"`, Confidence: `ConfidenceWeak`.

Add table-driven tests for each new check in `internal/analyzer/patterns_test.go`.

**Build:** `go build ./... && go vet ./... && go test ./internal/analyzer/ ./internal/store/`

---

### Issue #104 — LLM prompt enrichment with local pattern context

**Repo:** `wambozi/sigil`

**Read first:**
- `internal/analyzer/analyzer.go` — `buildPrompt()` and `localPass()`
- `internal/store/store.go` — `QueryTopFiles`, `QuerySuggestions`

**Acceptance criteria (from issue):**
- `buildPrompt()` includes: top edited files, detected pattern summaries, recent build success rate, AI usage patterns
- Prompt stays under 2000 tokens (≈ 8000 characters)
- Pattern context is summarized prose, not raw data (no file contents)

**What to build:**

*`internal/analyzer/analyzer.go`* — update `localPass()` to fetch additional context, add to `Summary`:

```go
type Summary struct {
    // existing fields...
    TopFiles        []store.FileEditCount  // add this field
    AcceptanceRate  float64                // add this field
    AIInteractions  []event.AIInteraction  // add this (last 20 interactions)
}
```

Update `localPass()` to populate these fields via `store.QueryTopFiles`, `store.QuerySuggestionAcceptanceRate`, `store.QueryAIInteractions`.

Rewrite `buildPrompt()` to produce richer prose:

```
Workflow summary for the past <period>:
Events: <file> file, <process> process, <git> git, <terminal> terminal, <ai> AI interactions

Top edited files: <file1> (<N> edits), <file2> (<N> edits), ...

Detected patterns:
- <pattern title>: <pattern body>
- ...

AI usage: <N> queries in this window, <acceptance>% suggestion acceptance rate.
Most common query category: <category>.

Build health: <N> build/test commands, <success_rate>% success rate.

What patterns do you notice? What might help this engineer?
```

Token guard: if the assembled prompt exceeds 7500 characters, truncate the patterns list to the top 3 and the files list to the top 3.

**Build:** `go build ./... && go vet ./... && go test ./internal/analyzer/`

---

### Issue #105 — Passive actuations: suggestion bar integration with confidence gating

**Repos:** `wambozi/sigil` (daemon) and `wambozi/sigil-os` (shell)

**Read first:**
- `internal/notifier/notifier.go` — `Surface()`, `Suggestion` type
- `cmd/sigild/main.go` — the `OnSummary` wrapping that currently fans out to socket subscribers (lines ~228–241)
- `shell/src/components/SuggestionBar.tsx` — existing implementation
- `shell/src-tauri/src/daemon_client.rs` — existing `subscribe_suggestions` function

**Acceptance criteria (from issue):**
- Pattern suggestions with confidence >= 0.6 surface in the suggestion bar (not just desktop notifications)
- Suggestion bar shows action hint when `ActionCmd` is set
- Tab on suggestion bar triggers the action (calls daemon feedback + executes ActionCmd in the active PTY)
- Suggestion bar history viewable via `sigilctl suggestions`

**Daemon changes (`wambozi/sigil`):**

*`internal/notifier/notifier.go`* — add a push callback:

```go
type Notifier struct {
    // ...existing fields...
    // OnSuggestion, if set, is called after every suggestion that passes the
    // confidence gate for the current level.  It receives the store-assigned ID
    // and the suggestion.  Must be non-blocking (hand off to goroutine if needed).
    OnSuggestion func(id int64, sg Suggestion)
}
```

In `Surface()`, after `InsertSuggestion` succeeds and the confidence threshold passes (i.e., `sg.Confidence >= ConfidenceModerate`), call `n.OnSuggestion(id, sg)` if non-nil. Do this before the level switch so it fires regardless of notification level.

*`cmd/sigild/main.go`* — replace the existing OnSummary fan-out block (~line 228) with a proper OnSuggestion hook on the notifier that includes the real store ID and action_cmd:

```go
ntf.OnSuggestion = func(id int64, sg notifier.Suggestion) {
    payload := socket.MarshalPayload(map[string]any{
        "id":         id,
        "text":       sg.Body,
        "title":      sg.Title,
        "confidence": sg.Confidence,
        "action_cmd": sg.ActionCmd,
    })
    srv.Notify("suggestions", payload)
}
```

Remove the wrapping of `anlz.OnSummary` that manually called `srv.Notify("suggestions", ...)` — it produced synthetic IDs and is now superseded.

**Shell changes (`wambozi/sigil-os`):**

*`shell/src/components/SuggestionBar.tsx`* — update the `Suggestion` interface and rendering:

```typescript
interface Suggestion {
  id: number          // real store ID (was string)
  text: string
  title: string
  confidence: number
  action_cmd: string  // empty string if no action
}
```

- When `action_cmd` is non-empty, append `• Tab to execute` to the hints.
- Tab handler: if `action_cmd` is set, after calling `daemon_feedback`, emit a Tauri event `execute-action` with payload `{ cmd: current.action_cmd }` so the active PTY can receive it.
- Add `sigilctl suggestions` link text at bottom right: a small `<button>` labeled "history" that, on click, executes `sigilctl suggestions` in the terminal PTY (emit `execute-action` with `cmd: "sigilctl suggestions"`).

**Commit strategy:** two commits — one in `wambozi/sigil`, one in `wambozi/sigil-os` — both with `closes #105`.

**Build (daemon):** `go build ./... && go vet ./... && go test ./internal/notifier/`
**Build (shell):** `npm run build` from `shell/` + `cargo build` from `shell/src-tauri/`

---

### Issue #111 — Split-pane support (Cmd+backslash, horizontal and vertical)

**Repo:** `wambozi/sigil-os`

**Read first:**
- `shell/src/components/ContentPane.tsx` — existing layout and keep-alive pattern
- `shell/src/context/AppContext.tsx` — `ViewId`, `AppState`
- `shell/src/app.tsx` — root layout

**Acceptance criteria (from issue):**
- `Ctrl+\` toggles split mode (horizontal by default)
- `Ctrl+Shift+\` toggles vertical split
- Each pane can have an independent tool view
- Focus switches between panes via keyboard shortcut
- Split state persists across tool switches within the session

**What to build:**

*`shell/src/layouts/index.ts`* — new file:

```typescript
export type SplitMode = 'none' | 'horizontal' | 'vertical'

export interface SplitState {
  mode: SplitMode
  primaryView: import('../context/AppContext').ViewId
  secondaryView: import('../context/AppContext').ViewId
  focus: 'primary' | 'secondary'
}
```

*`shell/src/context/AppContext.tsx`* — extend `AppState` with split state:

```typescript
interface AppState {
  // ...existing...
  split: SplitState
  setSplit: (s: SplitState) => void
}
```

Default split: `{ mode: 'none', primaryView: 'terminal', secondaryView: 'editor', focus: 'primary' }`.

*`shell/src/components/ContentPane.tsx`* — implement split-pane layout:

- Replace the stub `Ctrl+\` handler with a real toggle:
  - `Ctrl+\` → cycles `none → horizontal → none`
  - `Ctrl+Shift+\` → cycles `none → vertical → none`
  - `Ctrl+[` → focus primary pane; `Ctrl+]` → focus secondary pane
- In `horizontal` mode: render two `<div class="content-pane__split-pane">` side by side (50%/50%) each containing the tool view for `split.primaryView` / `split.secondaryView`.
- In `vertical` mode: stack them top/bottom.
- In `none` mode: existing single-view behavior.
- The focused pane gets a 1px accent-color border highlight.
- Navigation rail shortcuts (`Ctrl+1–6`) set the view for the **focused** pane only; the other pane's view stays unchanged.
- When `activeView` changes and mode is `none`, update `split.primaryView` to match.
- Export a `useSplitState()` hook for actuators (issue #106) to trigger split programmatically.

**Build:** `npm run build` from `shell/`

---

### Actuator Infrastructure (no issue — prereq for #106–#108)

**Repo:** `wambozi/sigil`

**Commit message:** `feat: actuator package infrastructure (prereq for #106-#108)`

**What to build:**

*`internal/actuator/actuator.go`* — package `actuator`:

```go
// Action is a single reversible actuation emitted by an Actuator.
type Action struct {
    ID          string        // unique within this daemon run; e.g. UUID or counter
    Description string        // human-readable
    UndoCmd     string        // shell command to reverse this action; empty if irreversible
    ExpiresAt   time.Time     // undo window (30s from creation)
}

// Actuator is implemented by each active actuation type.
type Actuator interface {
    Name() string
    // Check inspects current state and returns any Actions that should be taken.
    // Returning nil, nil means no action needed right now.
    Check(ctx context.Context) ([]Action, error)
}
```

*`internal/actuator/registry.go`* — `Registry` type:

```go
type Registry struct {
    actuators []Actuator
    notify    func(Action)   // called when an action is taken; wired to socket.Notify in main
    store     *store.Store
    log       *slog.Logger
}

func New(s *store.Store, notify func(Action), log *slog.Logger) *Registry

// Register adds an actuator to the registry.
func (r *Registry) Register(a Actuator)

// Run polls all registered actuators every 30 seconds until ctx is cancelled.
func (r *Registry) Run(ctx context.Context)
```

`Run` polls in a loop; for each actuator, calls `Check(ctx)`. For each returned `Action`, logs it via `store.InsertAction` and calls `r.notify(action)`.

*`internal/store/store.go`* — add `action_log` table to `migrate()` and add methods:

```sql
CREATE TABLE IF NOT EXISTS action_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    action_id   TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL,
    undo_cmd    TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    undone_at   INTEGER,
    expires_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_action_log_created ON action_log (created_at);
```

```go
func (s *Store) InsertAction(ctx context.Context, a actuator.Action) error
func (s *Store) QueryUndoableActions(ctx context.Context) ([]actuator.Action, error) // expires_at > now AND undone_at IS NULL
func (s *Store) MarkActionUndone(ctx context.Context, actionID string) error
```

Note: to avoid an import cycle (`store` importing `actuator`), define a `StoreAction` struct in `store` and have `actuator.Action` be a separate type. The mapping happens in `registry.go`. Or alternatively define the stored type in `store` package and have actuator types convert.

**Preferred approach:** define `store.ActionRecord` in the store package. `actuator.Action` is the domain type; `registry.go` converts between them (it imports both `store` and `actuator`).

**Build:** `go build ./... && go vet ./... && go test ./internal/actuator/`

---

### Issue #106 — Active actuation: auto-split pane when build starts

**Repos:** `wambozi/sigil` (daemon) and `wambozi/sigil-os` (shell)

**Read first:**
- `internal/actuator/actuator.go` and `registry.go` — just built
- `internal/collector/collector.go` — `Broadcast` channel
- `internal/analyzer/patterns.go` — `isTestOrBuildCmd` helper
- `shell/src/components/ContentPane.tsx` — split state implementation from #111
- `shell/src-tauri/src/daemon_client.rs` — existing subscribe_suggestions as model
- `cmd/sigild/main.go` — how collector.Broadcast is available (check the collector.New call)

**Acceptance criteria (from issue):**
- Daemon detects build/test command start via terminal event
- Sends split-pane actuation to shell over socket
- Shell splits content pane horizontally, showing build output in the new pane
- Auto-closes the split when the build completes (success or failure)
- User can opt out per-session via setting

**Daemon changes (`wambozi/sigil`):**

*`internal/actuator/build_split.go`* — `BuildSplitActuator`:

```go
// BuildSplitActuator watches the collector Broadcast channel for build/test
// command starts and emits split-pane and close-split actions.
type BuildSplitActuator struct {
    broadcast <-chan event.Event
    log       *slog.Logger
    // pendingBuild tracks whether a build is in progress to detect completion.
    pendingBuild bool
}
```

This actuator is event-driven, not poll-driven. It cannot use the `Actuator` interface's poll model directly. Instead, wire it as a goroutine in `main.go` that reads from `collector.Broadcast`, detects build start/end, and calls `registry.notify(Action{...})` directly.

Action for build start:
```go
Action{
    ID:          "build-split-" + uuid,
    Description: "Build started — split pane to show output",
    UndoCmd:     "",  // the close-split action reverses it
    ExpiresAt:   time.Now().Add(30 * time.Minute),
}
```

The socket notification payload for build start: `{"type":"split-pane","reason":"build_started"}`.
The socket notification payload for build end: `{"type":"close-split","reason":"build_done","exit_code":N}`.

These are pushed via `srv.Notify("actuations", payload)` (new topic).

**Socket in daemon:** register a new socket topic `actuations`. Wire in `main.go`:

```go
ntf.OnSuggestion = ...  // existing
// Wire actuation push:
actuatorNotify := func(a actuator.Action) {
    payload := socket.MarshalPayload(map[string]any{
        "type":        "actuation",
        "id":          a.ID,
        "description": a.Description,
        "undo_cmd":    a.UndoCmd,
    })
    srv.Notify("actuations", payload)
}
```

**Shell changes (`wambozi/sigil-os`):**

*`shell/src-tauri/src/daemon_client.rs`* — add `subscribe_actuations()` function (parallel to `subscribe_suggestions`):
- Same pattern: dedicated socket connection, subscribes to `"actuations"` topic, emits `daemon-actuation` Tauri events.

Register in `main.rs` setup alongside `subscribe_suggestions`.

*`shell/src/components/ContentPane.tsx`* — listen for `daemon-actuation` events:

```typescript
listen<ActuationPayload>('daemon-actuation', (event) => {
    const p = event.payload
    if (p.type === 'split-pane') {
        // Open horizontal split, secondary view = terminal
        setSplit({ mode: 'horizontal', primaryView: split.primaryView, secondaryView: 'terminal', focus: 'primary' })
    } else if (p.type === 'close-split') {
        setSplit({ ...split, mode: 'none' })
    }
})
```

**Build (daemon):** `go build ./... && go vet ./... && go test ./internal/actuator/`
**Build (shell):** `npm run build` + `cargo build`

---

### Issue #107 — Active actuation: pre-warm idle Docker containers

**Repo:** `wambozi/sigil`

**Read first:**
- `internal/actuator/actuator.go` and `registry.go`
- `internal/store/store.go` — `QueryTerminalEvents` for temporal session pattern
- `internal/analyzer/patterns.go` — `checkTimeOfDay` as reference for time-of-day analysis

**Acceptance criteria (from issue):**
- Daemon identifies containers associated with current project (via docker-compose.yml or labels)
- Pre-warm fires 2 minutes before the user's typical session start time
- Notification surfaced in suggestion bar
- User can opt out in config

**What to build:**

*`internal/actuator/container_warm.go`* — `ContainerWarmActuator`:

```go
type ContainerWarmActuator struct {
    store   *store.Store
    log     *slog.Logger
    enabled bool // from config
}
```

Implements `Actuator` interface. `Name()` returns `"container_warm"`.

`Check()` logic:
1. Call `store.QueryTerminalEvents(ctx, time.Now().Add(-7*24*time.Hour))` to get a week of terminal activity.
2. Bucket events by `Timestamp.Hour()` (same approach as `checkTimeOfDay`). Find the most common start-of-session hour (first event after a 2+ hour gap each day).
3. If `time.Now().Hour()` == `(typicalStartHour - 1 + 24) % 24` and `time.Now().Minute() >= 58`, this is the 2-minute warning window.
4. Scan `cfg.watchPaths` for `docker-compose.yml` files. For each, extract service names.
5. Run `docker ps -a --format "{{.Names}}\t{{.Status}}"` via `exec.Command` to find containers matching the compose service names that are stopped.
6. For each stopped container, return an `Action`:
   ```go
   Action{
       ID:          "container-warm-" + name,
       Description: "Pre-warming container: " + name,
       UndoCmd:     "docker stop " + name,
       ExpiresAt:   time.Now().Add(30 * time.Second),
   }
   ```
   `Check()` does NOT start the containers itself — the registry's notify callback handles that by calling `docker start <name>` via `os/exec`.

Wait — re-reading the issue: the daemon should pre-warm (start the containers). Keep the action model but execute via `os/exec` inside `Check()` before returning the action. The action is returned so it appears in the action log for undo.

Actually the cleaner design: `Check()` returns proposed actions; the registry executes them by calling `os/exec` for each action's implied command. Add an `ExecuteCmd string` field to `Action` (separate from `UndoCmd`). The registry calls `exec.Command("sh", "-c", a.ExecuteCmd)` for each non-empty `ExecuteCmd`.

Add `ExecuteCmd string` to `internal/actuator/actuator.go`'s `Action` struct.

*`internal/config/config.go`* — add `ActuationsEnabled bool` to the daemon config struct. Default: `true`.

**Build:** `go build ./... && go vet ./... && go test ./internal/actuator/`

---

### Issue #108 — Active actuation: dynamic keybinding profiles

**Repos:** `wambozi/sigil` (daemon) and `wambozi/sigil-os` (shell + nix)

**Read first:**
- `internal/actuator/actuator.go` and `registry.go`
- `internal/event/event.go` — `KindHyprland` events
- `shell/src/context/AppContext.tsx` — `ViewId` (tool names)
- `sigil-os/modules/` directory (currently empty)
- `sigil-os/flake.nix` — current minimal structure

**Acceptance criteria (from issue):**
- Daemon detects active tool (terminal, editor, browser, git)
- Keybinding profile loaded from Nix flake config per tool
- Profile switch is instant and non-disruptive
- User can see current active profile via `sigilctl status`

**Daemon changes (`wambozi/sigil`):**

*`internal/actuator/keybinding.go`* — `KeybindingActuator`:

The shell broadcasts `activeView` changes to the daemon via the `ingest` socket method (or a new `view-changed` socket method). Use an event-driven approach: register a `view-changed` socket handler in `main.go` that stores the current view, and have the actuator return a `keybinding-profile` action when the view changes.

Simpler: wire it in `main.go` — register a socket handler `view-changed` that publishes a `keybinding-profile` actuation immediately (no polling needed):

```go
srv.Handle("view-changed", func(ctx context.Context, req socket.Request) socket.Response {
    var p struct { View string `json:"view"` }
    if err := json.Unmarshal(req.Payload, &p); err != nil {
        return socket.Response{Error: "invalid payload"}
    }
    payload := socket.MarshalPayload(map[string]any{
        "type":    "keybinding-profile",
        "profile": p.View, // "terminal" | "editor" | "browser" | "git" | "containers" | "insights"
    })
    srv.Notify("actuations", payload)
    return socket.Response{OK: true}
})
```

Track current profile in `cmd/sigild/main.go` via `atomic.Value` and expose it via the `status` handler: `"current_keybinding_profile": currentProfile.Load()`.

**NixOS config (`wambozi/sigil-os`):**

*`modules/sigil-keybindings.nix`* — new NixOS module defining keybinding profiles per tool as options:

```nix
{ config, lib, ... }:
with lib;
{
  options.services.sigil-shell.keybindings = {
    enable = mkEnableOption "Sigil Shell dynamic keybinding profiles";
    profiles = mkOption {
      type = types.attrsOf (types.attrsOf types.str);
      default = {
        terminal = {};
        editor   = {};
        browser  = {};
        git      = {};
      };
      description = "Keybinding profiles per tool. Each value is an attrset of key -> action.";
    };
  };
}
```

**Shell changes (`wambozi/sigil-os`):**

*`shell/src/context/AppContext.tsx`* — when `setActiveView` is called, also call `invoke('daemon_view_changed', { view: newView })`.

*`shell/src-tauri/src/daemon_client.rs`* — add `daemon_view_changed(view: String)` Tauri command that sends `{"method":"view-changed","payload":{"view":"..."}}` to the daemon.

Register in `main.rs`.

**Build (daemon):** `go build ./... && go vet ./... && go test ./internal/actuator/`
**Build (shell):** `npm run build` + `cargo build`

---

### Issue #109 — Undo system (Cmd+Z at shell level, reversible action log)

**Repos:** `wambozi/sigil` (daemon) and `wambozi/sigil-os` (shell)

**Read first:**
- `internal/actuator/actuator.go` — `Action` type including `UndoCmd`
- `internal/store/store.go` — `action_log` table and methods from the actuator infra step
- `cmd/sigild/main.go` — registered socket handlers

**Acceptance criteria (from issue):**
- All active actuations produce an `UndoEntry` in the daemon action log
- `Ctrl+Z` in the shell sends an undo request to the daemon
- Daemon executes the undo command (from `Action.UndoCmd`)
- Undo is available for 30 seconds after the action
- `sigilctl` shows recent actions with undo status

**Daemon changes (`wambozi/sigil`):**

*`cmd/sigild/main.go`* — register `undo` and `actions` socket handlers:

```go
srv.Handle("undo", func(ctx context.Context, _ socket.Request) socket.Response {
    actions, err := db.QueryUndoableActions(ctx)
    if err != nil || len(actions) == 0 {
        return socket.Response{Error: "no undoable actions"}
    }
    // Take the most recent one.
    a := actions[len(actions)-1]
    if a.UndoCmd == "" {
        return socket.Response{Error: "action is not reversible"}
    }
    if err := exec.Command("sh", "-c", a.UndoCmd).Run(); err != nil {
        return socket.Response{Error: fmt.Sprintf("undo failed: %s", err)}
    }
    _ = db.MarkActionUndone(ctx, a.ID)
    return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
        "undone": a.Description,
    })}
})

srv.Handle("actions", func(ctx context.Context, _ socket.Request) socket.Response {
    actions, err := db.QueryUndoableActions(ctx)
    if err != nil {
        return socket.Response{Error: err.Error()}
    }
    return socket.Response{OK: true, Payload: socket.MarshalPayload(actions)}
})
```

**Shell changes (`wambozi/sigil-os`):**

*`shell/src-tauri/src/daemon_client.rs`* — add `daemon_undo()` Tauri command that sends `{"method":"undo","payload":{}}`.

*`shell/src/components/InputBar.tsx`* — add `Ctrl+Z` handler:
- When `Ctrl+Z` is pressed and the input bar is empty (no text being typed), call `invoke('daemon_undo')`. Show the result as a brief toast or in the suggestion bar.
- If input is non-empty, let the browser's default `Ctrl+Z` text editing behavior run.

Register `daemon_undo` in `main.rs`.

**sigilctl** — add `actions` subcommand to `cmd/sigilctl/main.go` that calls the `actions` socket method and prints recent undoable actions.

**Build (daemon):** `go build ./... && go vet ./... && go test ./internal/store/`
**Build (shell):** `npm run build` + `cargo build`

---

### Issue #110 — Progressive AI disclosure via suggestion bar

**Repo:** `wambozi/sigil`

**Read first:**
- `internal/analyzer/patterns.go` — existing detectors, including the new ones from #103
- `internal/store/store.go` — `QueryAIInteractions`
- `cmd/sigild/main.go` — `anlz.OnSummary` hook

**Acceptance criteria (from issue):**
- Tier 0: only pattern-based suggestions, no AI mode prompts
- Tier 1: occasionally surface "want to understand X? Alt+Tab and ask"
- Tier 2: proactive AI mode suggestions tied to real problems
- Tier 3: AI mode suggestions are rich, codebase-aware
- Tier progression stored in `patterns` table, updates as AI interaction events accumulate

**What to build:**

*`internal/analyzer/patterns.go`* — add `detectAITier` helper and `checkProgressiveDisclosure` detector:

```go
// AITier classifies the engineer's AI adoption level.
type AITier int

const (
    TierObserver   AITier = 0 // no AI queries
    TierExplorer   AITier = 1 // < 5 queries in last 7 days
    TierIntegrator AITier = 2 // 5–20 queries in last 7 days
    TierNative     AITier = 3 // 20+ queries in last 7 days
)

func detectAITier(interactions []event.AIInteraction) AITier
```

`checkProgressiveDisclosure` calls `store.QueryAIInteractions(ctx, since)`, computes the tier, then emits a contextual suggestion based on the tier and the current pattern context:

- **Tier 0→1:** if there are >= 3 build failures in the window (from `checkBuildFailureStreak` data), emit: `"Stuck on build failures? Try Alt+Tab and ask: 'why is my build failing?'"` — Category: `"ai_discovery"`, Confidence: `ConfidenceModerate`, ActionCmd: `""`.
- **Tier 1→2:** if `editThenTest` ratio is high (> 0.6), emit: `"You always run tests after edits in <dir>. Alt+Tab and ask: 'set up a file-watch test runner for this project.'"` — Category: `"ai_discovery"`, Confidence: `ConfidenceModerate`.
- **Tier 2→3:** emit codebase-aware prompts based on the top file from `checkFrequentFiles`: `"You're spending time in <file>. Alt+Tab and ask: 'summarize this module and suggest improvements.'"` — Category: `"ai_discovery"`, Confidence: `ConfidenceStrong`.
- **Tier 3:** no progressive disclosure — they're native. No suggestion emitted.

Store the current tier in the `patterns` table (`kind = "ai_tier"`) so it persists across restarts.

**Build:** `go build ./... && go vet ./... && go test ./internal/analyzer/`

---

### Issue #112 — Pop-out tool to Hyprland window

**Repo:** `wambozi/sigil-os`

**Read first:**
- `shell/src-tauri/src/main.rs` — Tauri command registration
- `shell/src-tauri/src/pty.rs` — PTY map structure
- `shell/src/components/LeftRail.tsx` — tool labels and keyboard shortcuts
- `shell/src/context/AppContext.tsx` — `ViewId`

**Acceptance criteria (from issue):**
- Keyboard shortcut pops the current tool out into a new Hyprland window
- Popped-out window maintains PTY connection (for terminal/editor)
- Pop-out window can be returned to shell content pane
- Hyprland window rules applied (floating)

**What to build:**

*`shell/src-tauri/src/hyprland.rs`* — new file:

```rust
use std::env;
use std::io::Write;
use std::os::unix::net::UnixStream;

/// Sends a Hyprland dispatch command via the Hyprland IPC socket.
fn hyprland_dispatch(cmd: &str) -> Result<(), String> {
    let sig = env::var("HYPRLAND_INSTANCE_SIGNATURE")
        .map_err(|_| "HYPRLAND_INSTANCE_SIGNATURE not set".to_string())?;
    let socket_path = format!("/tmp/hypr/{}/.socket.sock", sig);
    let mut stream = UnixStream::connect(&socket_path)
        .map_err(|e| format!("connect to Hyprland socket: {}", e))?;
    let frame = format!("dispatch {}", cmd);
    stream.write_all(frame.as_bytes())
        .map_err(|e| format!("write to Hyprland socket: {}", e))?;
    Ok(())
}

/// Pops the current tool out into a new Hyprland floating window.
/// For terminal/editor: spawns a new terminal with the current PTY's shell.
/// For other tools: spawns a new sigil-shell instance in that tool's view.
#[tauri::command]
pub fn pop_out_tool(tool: String) -> Result<(), String> {
    // Spawn the sigil-shell process (or a dedicated terminal) and
    // apply Hyprland floating window rules.
    let shell = env::var("SHELL").unwrap_or_else(|_| "/bin/bash".to_string());
    match tool.as_str() {
        "terminal" | "editor" => {
            // Open the user's terminal emulator for PTY tools.
            hyprland_dispatch(&format!("exec kitty --class sigil-popout {}", shell))?;
        }
        _ => {
            // For non-PTY tools, could open a minimal browser or git UI.
            // For now, open a kitty window with sigilctl status.
            hyprland_dispatch("exec kitty --class sigil-popout sigilctl status")?;
        }
    }
    // Apply floating rule for the new window class.
    hyprland_dispatch("exec hyprctl --batch 'keyword windowrulev2 float,class:sigil-popout'")?;
    Ok(())
}
```

*`shell/src-tauri/src/main.rs`* — add `mod hyprland;` and register `hyprland::pop_out_tool`.

*`shell/src/components/LeftRail.tsx`* — add a pop-out button (small icon) next to or below the active tool icon. `Ctrl+Shift+O` triggers pop-out. On click or shortcut, call `invoke('pop_out_tool', { tool: activeView })`.

**Build:** `cargo build` + `npm run build`

---

### Issue #113 — Command palette (Cmd+K)

**Repo:** `wambozi/sigil-os`

**Read first:**
- `shell/src/context/AppContext.tsx` — `ViewId`, app state
- `shell/src/app.tsx` — top-level layout, keyboard event wiring
- `shell/src-tauri/src/daemon_client.rs` — `daemon_files`, `daemon_commands` for palette items

**Acceptance criteria (from issue):**
- `Ctrl+K` opens palette overlay
- Searchable list: tool switch, sigilctl commands, recent files, recent commands, daemon actions
- Fuzzy search with ranked results
- Enter executes; Esc closes
- Keyboard-navigable

**What to build:**

*`shell/src/components/CommandPalette.tsx`* — new component:

```typescript
interface PaletteItem {
  id: string
  label: string
  description?: string
  action: () => void | Promise<void>
  score?: number  // fuzzy match score for sorting
}
```

Static items (always present):
- Tool switches: "Switch to Terminal", "Switch to Editor", etc. — call `setActiveView(view)`.
- Daemon actions: "Trigger analysis", "Purge local data", "View suggestions" — call `invoke(...)`.
- `sigilctl` subcommands: "sigilctl status", "sigilctl events", "sigilctl actions" — emit `execute-action` with the command string.

Dynamic items (loaded on open):
- Recent files from `daemon_files` (top 10) — execute in terminal: `nvim <path>` via PTY.
- Recent commands from `daemon_commands` (top 10) — execute in terminal via PTY.

Fuzzy search: simple `score(query, label)` function — award points for each character in `query` that appears in `label` in order (subsequence match). Sort by score descending; only show items with score > 0 when query is non-empty.

Rendering: full-screen overlay with a centered 600px-wide modal. Search input at top. Scrollable list below. Selected item highlighted with accent color. `Arrow Up/Down` navigates. `Enter` executes and closes. `Esc` closes without action.

*`shell/src/app.tsx`* — add `<CommandPalette>` to root render. Register `Ctrl+K` shortcut that sets `isPaletteOpen = true` in `AppContext`.

*`shell/src/context/AppContext.tsx`* — add `isPaletteOpen: boolean` and `setIsPaletteOpen` to `AppState`.

**Build:** `npm run build`

---

### Issue #114 — Theme customization via Nix flake

**Repos:** `wambozi/sigil-os` (nix module + shell CSS)

**Read first:**
- `shell/src/styles/global.css` — all hardcoded color values
- `sigil-os/flake.nix` — current flake structure
- `sigil-os/modules/.gitkeep` — the modules directory

**Acceptance criteria (from issue):**
- Nix flake exposes a theme option: colors, font family, font size, border radius
- Shell reads theme config at startup and applies CSS variables
- Minimum: dark default theme, light variant, one custom example
- IBM Plex Mono as default font

**What to build:**

*`shell/src/styles/global.css`* — replace all hardcoded colors/fonts with CSS variables:

```css
:root {
  --color-bg:          #0a0a0a;
  --color-fg:          #e5e5e5;
  --color-accent:      #6366f1;
  --color-border:      #222222;
  --color-bg-surface:  #111111;
  --color-bg-hover:    #1a1a1a;
  --color-fg-muted:    #888888;
  --font-family:       'IBM Plex Mono', monospace;
  --font-size:         13px;
  --border-radius:     4px;
}
```

Replace every hardcoded `#0a0a0a`, `#e5e5e5`, `#6366f1`, `#222222`, `#111111` throughout `global.css` with the corresponding variable.

*`shell/src-tauri/src/main.rs`* — on startup, check for a theme CSS file at `~/.config/sigil-shell/theme.css`. If it exists, read it and inject it into the WebView via Tauri's `eval` after app launch:

```rust
// In the setup closure or after run():
if let Ok(css) = std::fs::read_to_string(theme_path) {
    let js = format!("const s = document.createElement('style'); s.textContent = {}; document.head.appendChild(s);",
        serde_json::to_string(&css).unwrap_or_default());
    // Use app.eval or window.eval_script
}
```

Use Tauri 2.x `WebviewWindow::eval` for script injection at startup.

*`modules/sigil-shell.nix`* — NixOS module:

```nix
{ config, lib, pkgs, ... }:
with lib;
let cfg = config.services.sigil-shell;
in {
  options.services.sigil-shell = {
    enable = mkEnableOption "Sigil Shell";
    theme = {
      background    = mkOption { type = types.str; default = "#0a0a0a"; };
      foreground    = mkOption { type = types.str; default = "#e5e5e5"; };
      accent        = mkOption { type = types.str; default = "#6366f1"; };
      border        = mkOption { type = types.str; default = "#222222"; };
      surface       = mkOption { type = types.str; default = "#111111"; };
      fontFamily    = mkOption { type = types.str; default = "'IBM Plex Mono', monospace"; };
      fontSize      = mkOption { type = types.str; default = "13px"; };
      borderRadius  = mkOption { type = types.str; default = "4px"; };
    };
  };

  config = mkIf cfg.enable {
    home.file.".config/sigil-shell/theme.css".text = ''
      :root {
        --color-bg:         ${cfg.theme.background};
        --color-fg:         ${cfg.theme.foreground};
        --color-accent:     ${cfg.theme.accent};
        --color-border:     ${cfg.theme.border};
        --color-bg-surface: ${cfg.theme.surface};
        --font-family:      ${cfg.theme.fontFamily};
        --font-size:        ${cfg.theme.fontSize};
        --border-radius:    ${cfg.theme.borderRadius};
      }
    '';
  };
}
```

Also add a `light` theme example in `modules/themes/light.nix`:

```nix
# Example light theme — import into flake.nix and set services.sigil-shell.theme.*
{
  background   = "#f5f5f5";
  foreground   = "#1a1a1a";
  accent       = "#4f46e5";
  border       = "#e0e0e0";
  surface      = "#ffffff";
}
```

**Build:** `npm run build` + `cargo build`

---

### Issue #115 — Phase 3 exit criteria validation

**Repo:** `wambozi/sigil-os`

Create `phases/phase_3_intelligence_polish.md` with the following content:

```markdown
# Phase 3 — Intelligence, Actuator & Polish: Exit Criteria

## Status

- [ ] Three active actuations work: auto-split on build, container pre-warm, dynamic keybindings
      Verification: trigger a build command in the terminal — shell should split automatically;
      run daemon for 1+ day — containers should pre-warm before typical session start;
      switch tool views — keybinding profile should update.

- [ ] Suggestion acceptance rate above 60% (self-testing over 1 week)
      Verification: `sigilctl suggestions` — check accepted/(accepted+dismissed) ratio.

- [ ] Split-pane and pop-out to Hyprland window work reliably
      Verification: Ctrl+\ to split, Ctrl+Shift+O to pop out active tool.

- [ ] Command palette covers all sigilctl commands and tool switches
      Verification: Ctrl+K — verify all 6 tool switches and sigilctl subcommands appear.

- [ ] One external developer has tested the full system and provided feedback
      Verification: written feedback documented in docs/external-tester-feedback.md

- [ ] No regression in daemon memory (still under 50MB RSS)
      Verification: `sigilctl status` — check rss_mb field after 48h uptime.

## Implementation Notes

See linked issues: #103, #104, #105, #106, #107, #108, #109, #110, #111, #112, #113, #114.
```

Close issues #115 and #50 (the duplicate).

**Build:** no build required (docs only)

---

## Completion

After all issues are closed, verify the phase milestone:

```bash
gh api repos/wambozi/sigil/milestones --jq '.[] | select(.title | contains("Phase 3")) | {title, open_issues}'
```

If `open_issues` is 1 (only the epic #4 remains), close it:

```bash
gh issue close 4 --repo wambozi/sigil --comment "Phase 3 complete. All implementation issues closed."
```

Phase 3 is done.
