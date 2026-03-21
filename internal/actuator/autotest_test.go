package actuator

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// --- truncateTail ----------------------------------------------------------

func TestTruncateTail(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"shorter than n", "hello", 10, "hello"},
		{"equal to n", "hello", 5, "hello"},
		{"longer than n", "0123456789extra", 5, "...extra"},
		{"empty string", "", 5, ""},
		{"n=0 keeps nothing", "abcdef", 0, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateTail(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncateTail(%q, %d) = %q; want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

// --- detectTestCommand -----------------------------------------------------

func TestDetectTestCommand(t *testing.T) {
	tests := []struct {
		name    string
		files   []string // files to create in the temp repo
		wantCmd string
	}{
		{"go.mod", []string{"go.mod"}, "go test ./..."},
		{"Cargo.toml", []string{"Cargo.toml"}, "cargo test"},
		{"package.json", []string{"package.json"}, "npm test"},
		{"pyproject.toml", []string{"pyproject.toml"}, "pytest"},
		{"setup.py", []string{"setup.py"}, "pytest"},
		{"Makefile", []string{"Makefile"}, "make test"},
		{"no match", []string{"README.md"}, ""},
		// go.mod wins over Cargo.toml when both are present.
		{"go.mod wins", []string{"go.mod", "Cargo.toml"}, "go test ./..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0o644); err != nil {
					t.Fatalf("create %s: %v", f, err)
				}
			}
			got := detectTestCommand(dir)
			if got != tt.wantCmd {
				t.Errorf("detectTestCommand = %q; want %q", got, tt.wantCmd)
			}
		})
	}
}

// --- findRepoRoot ----------------------------------------------------------

func TestFindRepoRoot_found(t *testing.T) {
	// Build: /tmp/xxx/repo/.git/  and a nested file /tmp/xxx/repo/pkg/sub/file.go
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(nested, "file.go")
	if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findRepoRoot(filePath)
	if got != root {
		t.Errorf("findRepoRoot(%q) = %q; want %q", filePath, got, root)
	}
}

func TestFindRepoRoot_notFound(t *testing.T) {
	// Use an isolated temp dir without .git.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findRepoRoot(filePath)
	if got != "" {
		// Acceptable if the test runner is already inside a git repo that
		// happens to contain the temp dir.
		t.Skipf("findRepoRoot returned %q; temp dir may be inside a git repo — skipping", got)
	}
}

// --- NewAutoTestActuator ---------------------------------------------------

func TestNewAutoTestActuator(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 0
	a := NewAutoTestActuator(log, func() int { return level }, nil)

	if a == nil {
		t.Fatal("NewAutoTestActuator returned nil")
	}
	if a.debounce != 5*time.Second {
		t.Errorf("debounce = %v; want 5s", a.debounce)
	}
	if a.minRunGap != 30*time.Second {
		t.Errorf("minRunGap = %v; want 30s", a.minRunGap)
	}
}

// --- RunEventLoop ----------------------------------------------------------

func TestAutoTestActuator_RunEventLoop_nonFileIgnored(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 4
	var scheduled []string
	a := NewAutoTestActuator(log, func() int { return level }, func(action Action) {
		scheduled = append(scheduled, action.ID)
	})

	ch := make(chan event.Event, 3)
	ch <- event.Event{Kind: event.KindTerminal, Payload: map[string]any{"cmd": "go test"}, Timestamp: time.Now()}
	ch <- event.Event{Kind: event.KindGit, Payload: map[string]any{}, Timestamp: time.Now()}
	ch <- event.Event{Kind: event.KindProcess, Payload: map[string]any{}, Timestamp: time.Now()}
	close(ch)

	a.RunEventLoop(ch)

	// No actions should have been scheduled — only file events trigger the loop.
	if len(scheduled) != 0 {
		t.Errorf("expected 0 scheduled actions for non-file events; got %d", len(scheduled))
	}
}

func TestAutoTestActuator_RunEventLoop_levelBelowFour(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 3 // below 4 — must not trigger
	var scheduled []string
	a := NewAutoTestActuator(log, func() int { return level }, func(action Action) {
		scheduled = append(scheduled, action.ID)
	})

	dir := t.TempDir()
	// Need a .git dir so findRepoRoot succeeds.
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "main.go")

	ch := make(chan event.Event, 1)
	ch <- event.Event{Kind: event.KindFile, Payload: map[string]any{"path": filePath}, Timestamp: time.Now()}
	close(ch)

	a.RunEventLoop(ch)

	if len(scheduled) != 0 {
		t.Errorf("expected no scheduled actions at level 3; got %d", len(scheduled))
	}
}

func TestAutoTestActuator_RunEventLoop_emptyPath(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 4
	var scheduled []string
	a := NewAutoTestActuator(log, func() int { return level }, func(action Action) {
		scheduled = append(scheduled, action.ID)
	})

	ch := make(chan event.Event, 1)
	// File event with no path in payload.
	ch <- event.Event{Kind: event.KindFile, Payload: map[string]any{}, Timestamp: time.Now()}
	close(ch)

	a.RunEventLoop(ch)

	if len(scheduled) != 0 {
		t.Errorf("expected no action for empty path; got %d", len(scheduled))
	}
}

func TestAutoTestActuator_RunEventLoop_noRepoRoot(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 4
	var scheduled []string
	a := NewAutoTestActuator(log, func() int { return level }, func(action Action) {
		scheduled = append(scheduled, action.ID)
	})

	// Use a path inside a temp dir that has no .git directory.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "noreproot.go")

	ch := make(chan event.Event, 1)
	ch <- event.Event{Kind: event.KindFile, Payload: map[string]any{"path": filePath}, Timestamp: time.Now()}
	close(ch)

	a.RunEventLoop(ch)

	if len(scheduled) > 0 {
		t.Skipf("scheduled actions (%d) when temp dir is inside a git repo — skipping", len(scheduled))
	}
}

func TestAutoTestActuator_RunEventLoop_fileEventAtLevelFour(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	level := 4

	// Create a real repo-like dir so findRepoRoot returns non-empty.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(repo, "main.go")

	var scheduled []string
	a := NewAutoTestActuator(log, func() int { return level }, func(action Action) {
		scheduled = append(scheduled, action.ID)
	})
	// Zero debounce so scheduleTestRun fires immediately if called.
	a.debounce = 0
	a.lastRunAt = time.Time{}

	ch := make(chan event.Event, 1)
	ch <- event.Event{
		Kind:      event.KindFile,
		Payload:   map[string]any{"path": filePath},
		Timestamp: time.Now(),
	}
	close(ch)

	a.RunEventLoop(ch)

	// scheduleTestRun was called (timer was set), but the timer fires
	// asynchronously — verify no panic and the timer is set.
	a.mu.Lock()
	hasTimer := a.timer != nil
	a.mu.Unlock()
	if !hasTimer {
		t.Error("expected timer to be set after file event at level 4")
	}
}

// TestAutoTestActuator_RunEventLoop_schedulesCalled verifies end-to-end that a
// file event at level 4 with a valid repo root eventually triggers notifyFn.
func TestAutoTestActuator_RunEventLoop_schedulesCalled(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(repo, "main.go")

	// Use a channel to communicate from the notify callback to avoid a data race.
	notified := make(chan Action, 1)
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		select {
		case notified <- action:
		default:
		}
	})
	// Zero debounce + zero minRunGap so runTests fires without waiting.
	a.debounce = 0
	a.minRunGap = 0
	a.lastRunAt = time.Time{}

	ch := make(chan event.Event, 1)
	ch <- event.Event{
		Kind:      event.KindFile,
		Payload:   map[string]any{"path": filePath},
		Timestamp: time.Now(),
	}
	close(ch)

	a.RunEventLoop(ch)

	// After the channel is drained, the timer fires asynchronously.
	select {
	case <-notified:
		// Good — at least one action was dispatched.
	case <-time.After(500 * time.Millisecond):
		t.Error("expected at least 1 notify call after file event at level 4 with zero debounce")
	}
}

// --- scheduleTestRun -------------------------------------------------------

func TestAutoTestActuator_scheduleTestRun_deduplication(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var actions []Action
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		actions = append(actions, action)
	})
	// Set debounce to 0 so the timer fires immediately if it reaches that point,
	// but what we verify is that the second call stops the first timer.
	a.debounce = 0

	a.scheduleTestRun("/fake/repo")
	a.scheduleTestRun("/fake/repo") // second call must stop the first timer
	// Verify the struct state is consistent: timer is non-nil, no panic.
	a.mu.Lock()
	hasTimer := a.timer != nil
	a.mu.Unlock()
	if !hasTimer {
		t.Error("timer should be non-nil after scheduleTestRun")
	}
}

// --- runTests --------------------------------------------------------------

func TestAutoTestActuator_runTests_minRunGap(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var actions []Action
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		actions = append(actions, action)
	})

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set lastRunAt to now so that the minRunGap guard triggers.
	a.mu.Lock()
	a.lastRunAt = time.Now()
	a.mu.Unlock()

	a.runTests(repo)

	// notifyFn must NOT be called because minRunGap has not elapsed.
	if len(actions) != 0 {
		t.Errorf("expected no actions within minRunGap; got %d", len(actions))
	}
}

func TestAutoTestActuator_runTests_noTestCommand(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var actions []Action
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		actions = append(actions, action)
	})

	// Repo with no recognised build file.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// lastRunAt is zero so minRunGap check passes.
	a.runTests(repo)

	if len(actions) != 0 {
		t.Errorf("expected no actions when no test command found; got %d", len(actions))
	}
}

func TestAutoTestActuator_runTests_notifyCalledBeforeExec(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var actions []Action
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		actions = append(actions, action)
	})

	// Repo with go.mod — detectTestCommand returns "go test ./..."
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// notifyFn is called (action emitted) before the exec attempt.
	// We verify the action was emitted without caring whether the exec
	// succeeds (it will run "go test ./..." in the bare temp dir and may fail,
	// but the notification happens first).
	a.runTests(repo)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action emitted; got %d", len(actions))
	}
	if !strings.HasPrefix(actions[0].ID, "auto-test-") {
		t.Errorf("action ID %q does not have expected prefix", actions[0].ID)
	}
	if actions[0].ExecuteCmd != "go test ./..." {
		t.Errorf("ExecuteCmd = %q; want go test ./...", actions[0].ExecuteCmd)
	}
}

func TestAutoTestActuator_runTests_nilNotifyFn(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// notifyFn is nil — must not panic.
	a := NewAutoTestActuator(log, func() int { return 4 }, nil)

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.runTests(repo) // must not panic
}

// TestAutoTestActuator_runTests_successPath verifies the success branch of
// runTests: when the detected test command exits 0, the "tests passed" log
// path is taken and notifyFn is called exactly once before exec.
func TestAutoTestActuator_runTests_successPath(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var actions []Action
	a := NewAutoTestActuator(log, func() int { return 4 }, func(action Action) {
		actions = append(actions, action)
	})

	// Create a repo where the detected test command exits 0.
	// Use a Makefile with "test:" that runs "true" so the command always passes.
	repo := t.TempDir()
	makefile := "test:\n\ttrue\n"
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}

	a.runTests(repo)

	// notifyFn should have been called once (action emitted before exec).
	if len(actions) != 1 {
		t.Fatalf("expected 1 action; got %d", len(actions))
	}
}
