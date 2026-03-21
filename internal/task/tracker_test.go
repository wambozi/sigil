package task

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/store"
	storemocks "github.com/wambozi/sigil/internal/store/mocks"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// nopLogger returns a logger that discards all output.
func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 100}))
}

// newMockStore returns a MockReadWriter with UpdateTask and InsertTask stubbed
// to return nil so the tracker can persist freely in tests that don't care
// about store interactions.
func newMockStore(t *testing.T) *storemocks.MockReadWriter {
	t.Helper()
	m := storemocks.NewMockReadWriter(t)
	return m
}

// allowAnyPersist stubs UpdateTask (return nil) and InsertTask (return nil) on
// a mock so the tracker can call persist() without failing.
func allowAnyPersist(m *storemocks.MockReadWriter) {
	m.On("UpdateTask", mock.Anything, mock.Anything).Return(nil).Maybe()
	m.On("InsertTask", mock.Anything, mock.Anything).Return(nil).Maybe()
}

// gitRepoDir creates a temporary directory with a fake .git/HEAD file pointing
// at the given branch name.  Returns the repo root path.
func gitRepoDir(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))
	content := "ref: refs/heads/" + branch + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(content), 0o644))
	return dir
}

// fileEvent returns a KindFile event with the given path.
func fileEvent(path string) event.Event {
	return event.Event{
		Kind:      event.KindFile,
		Timestamp: time.Now(),
		Payload:   map[string]any{"path": path},
	}
}

// gitCommitEvent returns a KindGit commit event for the given repo.
func gitCommitEvent(repoRoot string) event.Event {
	return event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "commit", "repo_root": repoRoot},
	}
}

// testCmdEvent returns a terminal event for a test command with the given exit code.
func testCmdEvent(exitCode int) event.Event {
	return event.Event{
		Kind:      event.KindTerminal,
		Timestamp: time.Now(),
		Payload:   map[string]any{"cmd": "go test ./...", "exit_code": float64(exitCode)},
	}
}

// ---------------------------------------------------------------------------
// NewTracker / SetMLEngine
// ---------------------------------------------------------------------------

func TestNewTracker(t *testing.T) {
	m := newMockStore(t)
	tr := NewTracker(m, nopLogger())
	require.NotNil(t, tr)
	assert.Nil(t, tr.Current())
}

func TestSetMLEngine(t *testing.T) {
	m := newMockStore(t)
	tr := NewTracker(m, nopLogger())
	pred := &mockMLPredictor{enabled: true}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 5)
	assert.Equal(t, pred, tr.mlPredictor)
	assert.Equal(t, "/tmp/sigil.db", tr.dbPath)
	assert.Equal(t, 5, tr.retrainEvery)
}

// ---------------------------------------------------------------------------
// Current (snapshot copy)
// ---------------------------------------------------------------------------

func TestCurrent_nil(t *testing.T) {
	m := newMockStore(t)
	tr := NewTracker(m, nopLogger())
	assert.Nil(t, tr.Current())
}

func TestCurrent_returnsCopy(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	// Trigger task creation via a file event.
	e := fileEvent("/tmp/proj/main.go")
	tr.Process(ctx, e)

	snap1 := tr.Current()
	require.NotNil(t, snap1)

	// Mutate the copy — internal state must not change.
	snap1.FilesTouched["injected"] = 99
	snap2 := tr.Current()
	_, injected := snap2.FilesTouched["injected"]
	assert.False(t, injected, "Current() must return an independent copy")
}

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

func TestRestore_nilRecord(t *testing.T) {
	m := newMockStore(t)
	m.On("QueryCurrentTask", mock.Anything).Return((*store.TaskRecord)(nil), nil)
	tr := NewTracker(m, nopLogger())
	require.NoError(t, tr.Restore(context.Background()))
	assert.Nil(t, tr.Current())
}

func TestRestore_withRecord(t *testing.T) {
	now := time.Now()
	rec := &store.TaskRecord{
		ID:           "t_123",
		RepoRoot:     "/home/dev/proj",
		Branch:       "main",
		Phase:        string(PhaseEditing),
		Files:        map[string]int{"/home/dev/proj/main.go": 3},
		StartedAt:    now.Add(-10 * time.Minute),
		LastActivity: now,
		CommitCount:  1,
		TestRuns:     2,
		TestFailures: 0,
	}
	m := newMockStore(t)
	m.On("QueryCurrentTask", mock.Anything).Return(rec, nil)
	tr := NewTracker(m, nopLogger())
	require.NoError(t, tr.Restore(context.Background()))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, "t_123", snap.ID)
	assert.Equal(t, PhaseEditing, snap.Phase)
	assert.Equal(t, "main", snap.Branch)
	assert.Equal(t, 1, snap.CommitCount)
}

func TestRestore_storeError(t *testing.T) {
	m := newMockStore(t)
	m.On("QueryCurrentTask", mock.Anything).Return((*store.TaskRecord)(nil), errors.New("db unavailable"))
	tr := NewTracker(m, nopLogger())
	err := tr.Restore(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task tracker: restore")
}

// ---------------------------------------------------------------------------
// recordToTask / taskToRecord round-trip
// ---------------------------------------------------------------------------

func TestRecordToTask_taskToRecord_roundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	completed := now.Add(5 * time.Minute)
	original := &Task{
		ID:           "t_999",
		RepoRoot:     "/repo",
		Branch:       "feature/x",
		Phase:        PhaseCompleting,
		FilesTouched: map[string]int{"a.go": 2, "b.go": 1},
		StartedAt:    now,
		LastActivity: now.Add(time.Minute),
		CompletedAt:  &completed,
		CommitCount:  3,
		TestRuns:     4,
		TestFailures: 1,
	}

	rec := taskToRecord(original)
	restored := recordToTask(&rec)

	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.RepoRoot, restored.RepoRoot)
	assert.Equal(t, original.Branch, restored.Branch)
	assert.Equal(t, original.Phase, restored.Phase)
	assert.Equal(t, original.FilesTouched, restored.FilesTouched)
	assert.Equal(t, original.CommitCount, restored.CommitCount)
	assert.Equal(t, original.TestRuns, restored.TestRuns)
	assert.Equal(t, original.TestFailures, restored.TestFailures)
	require.NotNil(t, restored.CompletedAt)
	assert.Equal(t, *original.CompletedAt, *restored.CompletedAt)
}

// ---------------------------------------------------------------------------
// Process: irrelevant events are dropped
// ---------------------------------------------------------------------------

func TestProcess_irrelevantEventDropped(t *testing.T) {
	m := newMockStore(t)
	// No persist calls should happen for irrelevant events.
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	e := event.Event{Kind: event.KindProcess, Payload: map[string]any{}}
	tr.Process(ctx, e)
	assert.Nil(t, tr.Current())
}

func TestProcess_hyprlandDropped(t *testing.T) {
	m := newMockStore(t)
	tr := NewTracker(m, nopLogger())
	e := event.Event{Kind: event.KindHyprland, Payload: map[string]any{}}
	tr.Process(context.Background(), e)
	assert.Nil(t, tr.Current())
}

// ---------------------------------------------------------------------------
// Process: task creation
// ---------------------------------------------------------------------------

func TestProcess_startsTaskOnFileEdit(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	e := fileEvent("/tmp/repo/main.go")
	tr.Process(ctx, e)

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseEditing, snap.Phase)
}

func TestProcess_idleTimeoutWithNoCurrentTask_isNoop(t *testing.T) {
	m := newMockStore(t)
	// No persist calls expected.
	tr := NewTracker(m, nopLogger())

	// The actual idle timeout is triggered internally. Verify that with no
	// current task and no events, current remains nil.
	assert.Nil(t, tr.Current())
}

// ---------------------------------------------------------------------------
// Process: phase transitions
// ---------------------------------------------------------------------------

func TestProcess_editingTestPassStaysEditing(t *testing.T) {
	// In Process, SignalTestCmd is immediately replaced with ClassifyTerminalResult
	// before Transition is called.  Transition(editing, SignalTestPass) has no
	// match in the editing switch, so the phase stays editing.
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	assert.Equal(t, PhaseEditing, tr.Current().Phase)

	tr.Process(ctx, testCmdEvent(0)) // SignalTestCmd → resolved to SignalTestPass
	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseEditing, snap.Phase)
	assert.Equal(t, 1, snap.TestRuns)
	assert.Equal(t, 0, snap.TestFailures)
}

func TestProcess_testFailIncrementsFailures(t *testing.T) {
	// SignalTestCmd is resolved to SignalTestFail via ClassifyTerminalResult;
	// TestFailures is incremented and TestRuns is incremented.
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	tr.Process(ctx, testCmdEvent(1)) // fail
	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, 1, snap.TestFailures)
	assert.Equal(t, 1, snap.TestRuns)
	// Phase stays editing (Transition(editing, SignalTestFail) has no match).
	assert.Equal(t, PhaseEditing, snap.Phase)
}

func TestProcess_testPassResetsFailures(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	tr.Process(ctx, testCmdEvent(1)) // fail
	tr.Process(ctx, testCmdEvent(1)) // fail
	tr.Process(ctx, testCmdEvent(0)) // pass

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, 0, snap.TestFailures, "test pass must reset failure counter")
	assert.Equal(t, 3, snap.TestRuns)
}

func TestProcess_consecutiveFailuresEscalateToStuck(t *testing.T) {
	// The stuck escalation check fires when Transition returns PhaseVerifying AND
	// TestFailures >= stuckThreshold.  To reach this path, the task must already
	// be in PhaseVerifying (restored from store) so that Transition(verifying,
	// SignalTestFail) returns PhaseVerifying, triggering the escalation.
	now := time.Now()
	rec := &store.TaskRecord{
		ID:           "t_stuck",
		RepoRoot:     "/repo",
		Phase:        string(PhaseVerifying),
		Files:        map[string]int{},
		StartedAt:    now.Add(-5 * time.Minute),
		LastActivity: now,
		TestFailures: stuckThreshold - 1, // one more failure will hit the threshold
	}

	m := newMockStore(t)
	m.On("QueryCurrentTask", mock.Anything).Return(rec, nil)
	allowAnyPersist(m)

	tr := NewTracker(m, nopLogger())
	require.NoError(t, tr.Restore(context.Background()))

	tr.Process(context.Background(), testCmdEvent(1)) // fail → TestFailures == stuckThreshold

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseStuck, snap.Phase, "3 consecutive failures must escalate to stuck")
	assert.Equal(t, stuckThreshold, snap.TestFailures)
}

func TestProcess_stuckResolvesOnFileEdit(t *testing.T) {
	// Restore a task in PhaseStuck, then verify a file edit transitions to editing.
	now := time.Now()
	rec := &store.TaskRecord{
		ID:           "t_stuck",
		RepoRoot:     "/repo",
		Phase:        string(PhaseStuck),
		Files:        map[string]int{},
		StartedAt:    now.Add(-5 * time.Minute),
		LastActivity: now,
		TestFailures: stuckThreshold,
	}

	m := newMockStore(t)
	m.On("QueryCurrentTask", mock.Anything).Return(rec, nil)
	allowAnyPersist(m)

	tr := NewTracker(m, nopLogger())
	require.NoError(t, tr.Restore(context.Background()))
	require.Equal(t, PhaseStuck, tr.Current().Phase)

	tr.Process(context.Background(), fileEvent("/tmp/r/main.go"))
	assert.Equal(t, PhaseEditing, tr.Current().Phase)
}

func TestProcess_commitTracked(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	tr.Process(ctx, gitCommitEvent("")) // completing phase
	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, 1, snap.CommitCount)
	assert.Equal(t, PhaseCompleting, snap.Phase)
}

func TestProcess_fileEditTracked(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	tr.Process(ctx, fileEvent("/tmp/r/a.go"))
	tr.Process(ctx, fileEvent("/tmp/r/a.go"))
	tr.Process(ctx, fileEvent("/tmp/r/b.go"))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, 2, snap.FilesTouched["/tmp/r/a.go"])
	assert.Equal(t, 1, snap.FilesTouched["/tmp/r/b.go"])
}

func TestProcess_fileEditWithEmptyPathNotTracked(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	e := event.Event{
		Kind:      event.KindFile,
		Timestamp: time.Now(),
		Payload:   map[string]any{"path": ""},
	}
	tr.Process(ctx, e)

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Empty(t, snap.FilesTouched)
}

// ---------------------------------------------------------------------------
// Process: task completion (completing + commit → idle → nil current)
// ---------------------------------------------------------------------------

func TestProcess_completingPlusCommitCompletesTask(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	// editing → completing (via git staging)
	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	stagingEvent := event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "index_change", "repo_root": ""},
	}
	tr.Process(ctx, stagingEvent)
	require.Equal(t, PhaseCompleting, tr.Current().Phase)

	// completing + commit → idle → task completed → current nil
	tr.Process(ctx, gitCommitEvent(""))
	assert.Nil(t, tr.Current(), "task should be nil after completing→idle transition")
}

// ---------------------------------------------------------------------------
// Process: repo switch completes current task, starts new one
// ---------------------------------------------------------------------------

func TestProcess_repoSwitchStartsNewTask(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	repoA := t.TempDir()
	repoB := t.TempDir()

	eA := event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "commit", "repo_root": repoA},
	}
	eB := event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now().Add(time.Second),
		Payload:   map[string]any{"git_kind": "commit", "repo_root": repoB},
	}

	tr.Process(ctx, eA)
	firstID := tr.Current().ID

	tr.Process(ctx, eB)
	snap := tr.Current()
	require.NotNil(t, snap)
	assert.NotEqual(t, firstID, snap.ID, "repo switch must start a new task")
	assert.Equal(t, repoB, snap.RepoRoot)
}

// ---------------------------------------------------------------------------
// Process: OnTransition callback
// ---------------------------------------------------------------------------

func TestProcess_onTransitionCallback(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	var mu sync.Mutex
	var recorded []struct{ old, new Phase }

	tr.OnTransition = func(old, new Phase, _ *Task) {
		mu.Lock()
		recorded = append(recorded, struct{ old, new Phase }{old, new})
		mu.Unlock()
	}

	tr.Process(ctx, fileEvent("/tmp/r/main.go"))

	// Allow the goroutine to fire.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, recorded)
	assert.Equal(t, PhaseIdle, recorded[0].old)
	assert.Equal(t, PhaseEditing, recorded[0].new)
}

// ---------------------------------------------------------------------------
// Process: timestamp zero-value falls back to time.Now()
// ---------------------------------------------------------------------------

func TestProcess_zeroTimestampFallsBackToNow(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	before := time.Now()
	e := event.Event{
		Kind:    event.KindFile,
		Payload: map[string]any{"path": "/tmp/r/main.go"},
		// Timestamp is zero
	}
	tr.Process(ctx, e)
	after := time.Now()

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.True(t, !snap.LastActivity.Before(before) && !snap.LastActivity.After(after),
		"LastActivity should be between before and after when timestamp is zero")
}

// ---------------------------------------------------------------------------
// Process: branch switch updates Branch field
// ---------------------------------------------------------------------------

func TestProcess_branchSwitchUpdatesBranch(t *testing.T) {
	repo := gitRepoDir(t, "feature/new-thing")

	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	// Start a task in the repo.
	tr.Process(ctx, event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "commit", "repo_root": repo},
	})

	// Trigger a branch switch.
	tr.Process(ctx, event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now().Add(time.Second),
		Payload:   map[string]any{"git_kind": "head_change", "repo_root": repo},
	})

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, "feature/new-thing", snap.Branch)
}

// ---------------------------------------------------------------------------
// Process: persist falls back to InsertTask when UpdateTask returns error
// ---------------------------------------------------------------------------

func TestProcess_persistFallsBackToInsert(t *testing.T) {
	m := newMockStore(t)
	// UpdateTask always errors → InsertTask should be called.
	m.On("UpdateTask", mock.Anything, mock.Anything).Return(errors.New("not found"))
	m.On("InsertTask", mock.Anything, mock.Anything).Return(nil)

	tr := NewTracker(m, nopLogger())
	tr.Process(context.Background(), fileEvent("/tmp/r/main.go"))

	m.AssertCalled(t, "InsertTask", mock.Anything, mock.Anything)
}

// ---------------------------------------------------------------------------
// RunEventLoop
// ---------------------------------------------------------------------------

func TestRunEventLoop_closedChannelReturns(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	ch := make(chan event.Event)
	done := make(chan struct{})
	go func() {
		tr.RunEventLoop(ctx, ch)
		close(done)
	}()

	close(ch)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunEventLoop did not return after channel close")
	}
}

func TestRunEventLoop_ctxCancelReturns(t *testing.T) {
	m := newMockStore(t)
	tr := NewTracker(m, nopLogger())
	ctx, cancel := context.WithCancel(context.Background())

	ch := make(chan event.Event) // never written to
	done := make(chan struct{})
	go func() {
		tr.RunEventLoop(ctx, ch)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunEventLoop did not return after context cancellation")
	}
}

func TestRunEventLoop_processesEvents(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	ctx := context.Background()

	ch := make(chan event.Event, 2)
	ch <- fileEvent("/tmp/r/main.go")
	ch <- fileEvent("/tmp/r/util.go")
	close(ch)

	tr.RunEventLoop(ctx, ch)

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, 1, snap.FilesTouched["/tmp/r/main.go"])
	assert.Equal(t, 1, snap.FilesTouched["/tmp/r/util.go"])
}

// ---------------------------------------------------------------------------
// readBranch
// ---------------------------------------------------------------------------

func TestReadBranch_validHEAD(t *testing.T) {
	repo := gitRepoDir(t, "main")
	got := readBranch(repo)
	assert.Equal(t, "main", got)
}

func TestReadBranch_detachedHEAD(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))
	// Detached HEAD: content is a SHA, not a ref.
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"),
		[]byte("abc123def456\n"), 0o644))
	got := readBranch(dir)
	assert.Equal(t, "", got, "detached HEAD must return empty string")
}

func TestReadBranch_missingGitDir(t *testing.T) {
	dir := t.TempDir()
	got := readBranch(dir)
	assert.Equal(t, "", got, "missing .git must return empty string")
}

func TestReadBranch_nestedBranchName(t *testing.T) {
	repo := gitRepoDir(t, "feature/my-feature")
	got := readBranch(repo)
	assert.Equal(t, "feature/my-feature", got)
}

// ---------------------------------------------------------------------------
// RepoFromEvent: file event with real filesystem
// ---------------------------------------------------------------------------

func TestRepoFromEvent_fileEventFindsRepoRoot(t *testing.T) {
	repo := gitRepoDir(t, "main")
	// Create a subdirectory and a file within it.
	sub := filepath.Join(repo, "pkg", "util")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	filePath := filepath.Join(sub, "util.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package util\n"), 0o644))

	e := event.Event{
		Kind:    event.KindFile,
		Payload: map[string]any{"path": filePath},
	}
	got := RepoFromEvent(e)
	assert.Equal(t, repo, got)
}

func TestRepoFromEvent_fileEventNoGitDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "orphan.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package main\n"), 0o644))

	e := event.Event{
		Kind:    event.KindFile,
		Payload: map[string]any{"path": filePath},
	}
	got := RepoFromEvent(e)
	assert.Equal(t, "", got, "file outside a git repo must return empty string")
}

func TestRepoFromEvent_fileEventEmptyPath(t *testing.T) {
	e := event.Event{
		Kind:    event.KindFile,
		Payload: map[string]any{"path": ""},
	}
	got := RepoFromEvent(e)
	assert.Equal(t, "", got)
}

func TestRepoFromEvent_terminalEvent(t *testing.T) {
	e := event.Event{
		Kind:    event.KindTerminal,
		Payload: map[string]any{"cmd": "go test ./..."},
	}
	got := RepoFromEvent(e)
	assert.Equal(t, "", got, "terminal event has no repo root")
}

// ---------------------------------------------------------------------------
// ML predictor: early stuck escalation
// ---------------------------------------------------------------------------

// mlVerifyingTask returns a Tracker restored into PhaseVerifying with the given
// test failure count, wired to the provided mock store.
func mlVerifyingTask(t *testing.T, m *storemocks.MockReadWriter, failures int) *Tracker {
	t.Helper()
	now := time.Now()
	rec := &store.TaskRecord{
		ID:           "t_ml",
		RepoRoot:     "/repo",
		Phase:        string(PhaseVerifying),
		Files:        map[string]int{"/repo/main.go": 1},
		StartedAt:    now.Add(-10 * time.Minute),
		LastActivity: now,
		TestFailures: failures,
	}
	m.On("QueryCurrentTask", mock.Anything).Return(rec, nil)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())
	require.NoError(t, tr.Restore(context.Background()))
	return tr
}

func TestProcess_mlPredictorEarlyStuck(t *testing.T) {
	// ML predictor returns probability > 0.7 while in PhaseVerifying with
	// failures > 0 — should escalate to stuck before the heuristic threshold.
	m := newMockStore(t)
	tr := mlVerifyingTask(t, m, 1) // 1 failure, below heuristic threshold of 3

	pred := &mockMLPredictor{
		enabled:     true,
		probability: 0.9, // above 0.7 threshold
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 0)

	// One more failure from verifying: Transition(verifying, SignalTestFail) →
	// verifying; ML check fires and escalates.
	tr.Process(context.Background(), testCmdEvent(1))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseStuck, snap.Phase, "ML predictor above 0.7 should escalate to stuck early")
}

func TestProcess_mlPredictorBelowThreshold(t *testing.T) {
	m := newMockStore(t)
	tr := mlVerifyingTask(t, m, 1)

	pred := &mockMLPredictor{
		enabled:     true,
		probability: 0.5, // below 0.7 threshold
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 0)

	tr.Process(context.Background(), testCmdEvent(1))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseVerifying, snap.Phase, "ML below threshold must not escalate to stuck")
}

func TestProcess_mlPredictorDisabled(t *testing.T) {
	m := newMockStore(t)
	tr := mlVerifyingTask(t, m, 1)

	pred := &mockMLPredictor{
		enabled:     false,
		probability: 0.99,
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 0)

	tr.Process(context.Background(), testCmdEvent(1))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseVerifying, snap.Phase, "disabled ML predictor must not influence phase")
}

func TestProcess_mlPredictorError_failOpen(t *testing.T) {
	m := newMockStore(t)
	tr := mlVerifyingTask(t, m, 1)

	pred := &mockMLPredictor{
		enabled:    true,
		predictErr: errors.New("model not ready"),
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 0)

	tr.Process(context.Background(), testCmdEvent(1))

	snap := tr.Current()
	require.NotNil(t, snap)
	// Should fall through to heuristics (only 2 failures, not stuck yet).
	assert.Equal(t, PhaseVerifying, snap.Phase, "ML error must fail-open (heuristics still apply)")
}

// ---------------------------------------------------------------------------
// ML retraining trigger
// ---------------------------------------------------------------------------

func TestCompleteCurrentTask_triggersRetrainAfterN(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())

	trained := make(chan struct{}, 1)
	pred := &mockMLPredictor{
		enabled: true,
		trainFn: func() { trained <- struct{}{} },
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 1) // retrain after every 1 completed task

	ctx := context.Background()

	// Complete a task: edit → completing → commit → idle
	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	stagingEvt := event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "index_change", "repo_root": ""},
	}
	tr.Process(ctx, stagingEvt)
	tr.Process(ctx, gitCommitEvent(""))
	// current should be nil now (task completed)

	select {
	case <-trained:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected ML retraining to be triggered after task completion")
	}
}

// ---------------------------------------------------------------------------
// persist: both UpdateTask and InsertTask fail
// ---------------------------------------------------------------------------

func TestProcess_persistBothStoreFail(t *testing.T) {
	// When UpdateTask and InsertTask both return errors, the tracker logs an
	// error but does not panic or return an error to the caller.
	m := newMockStore(t)
	m.On("UpdateTask", mock.Anything, mock.Anything).Return(errors.New("update failed"))
	m.On("InsertTask", mock.Anything, mock.Anything).Return(errors.New("insert failed"))

	tr := NewTracker(m, nopLogger())
	// This should not panic.
	require.NotPanics(t, func() {
		tr.Process(context.Background(), fileEvent("/tmp/r/main.go"))
	})
}

// ---------------------------------------------------------------------------
// completeCurrentTask: retraining with train error is logged, not returned
// ---------------------------------------------------------------------------

func TestCompleteCurrentTask_retrainError_nocrash(t *testing.T) {
	m := newMockStore(t)
	allowAnyPersist(m)
	tr := NewTracker(m, nopLogger())

	errored := make(chan struct{}, 1)
	pred := &mockMLPredictor{
		enabled:  true,
		trainFn:  func() { errored <- struct{}{} },
		trainErr: errors.New("retrain failed"),
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 1)

	ctx := context.Background()
	tr.Process(ctx, fileEvent("/tmp/r/main.go"))
	stagingEvt := event.Event{
		Kind:      event.KindGit,
		Timestamp: time.Now(),
		Payload:   map[string]any{"git_kind": "index_change", "repo_root": ""},
	}
	tr.Process(ctx, stagingEvt)
	tr.Process(ctx, gitCommitEvent(""))

	select {
	case <-errored:
		// train was called; error was handled internally
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected retrain to be attempted")
	}
}

// ---------------------------------------------------------------------------
// predictStuck: result map missing probability key (wrong type)
// ---------------------------------------------------------------------------

func TestProcess_mlPredictorNoProbabilityKey(t *testing.T) {
	// Predictor returns a result without the "probability" key — should
	// fail-open (return 0) and not escalate to stuck.
	m := newMockStore(t)
	tr := mlVerifyingTask(t, m, 1)

	pred := &mockMLPredictor{
		enabled:         true,
		wrongTypeResult: true,
	}
	tr.SetMLEngine(pred, "/tmp/sigil.db", 0)

	tr.Process(context.Background(), testCmdEvent(1))

	snap := tr.Current()
	require.NotNil(t, snap)
	assert.Equal(t, PhaseVerifying, snap.Phase, "missing probability key must fail-open")
}

// ---------------------------------------------------------------------------
// mockMLPredictor — local stub satisfying MLPredictor interface
// ---------------------------------------------------------------------------

type mockMLPredictor struct {
	enabled         bool
	probability     float64
	predictErr      error
	trainFn         func()
	trainErr        error
	wrongTypeResult bool
}

func (p *mockMLPredictor) Predict(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	if p.predictErr != nil {
		return nil, p.predictErr
	}
	if p.wrongTypeResult {
		// Return a result without a float64 "probability" field.
		return map[string]any{"probability": "not-a-float"}, nil
	}
	return map[string]any{"probability": p.probability}, nil
}

func (p *mockMLPredictor) Train(_ context.Context, _ string) error {
	if p.trainFn != nil {
		p.trainFn()
	}
	return p.trainErr
}

func (p *mockMLPredictor) Enabled() bool {
	return p.enabled
}
