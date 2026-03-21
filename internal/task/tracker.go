package task

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/store"
)

const (
	// idleTimeout is the duration of inactivity before a task transitions to idle.
	idleTimeout = 15 * time.Minute

	// stuckThreshold is the number of consecutive test failures before the
	// verifying phase escalates to stuck.
	stuckThreshold = 3
)

// MLPredictor is an optional interface for ML-powered predictions.
// The ml.Engine satisfies this interface when passed via SetMLEngine.
type MLPredictor interface {
	Predict(ctx context.Context, endpoint string, features map[string]any) (map[string]any, error)
	Train(ctx context.Context, dbPath string) error
	Enabled() bool
}

// TransitionCallback is called on significant phase transitions.
// The callback receives the old phase, new phase, and current task snapshot.
type TransitionCallback func(oldPhase, newPhase Phase, task *Task)

// Tracker maintains the current task state and processes events in real time.
// It runs an event loop reading from a broadcast channel and updates the
// store on every phase transition.
type Tracker struct {
	store store.ReadWriter
	log   *slog.Logger

	mu      sync.Mutex
	current *Task

	idleTimer      *time.Timer
	mlPredictor    MLPredictor
	dbPath         string
	retrainEvery   int // retrain after N completed tasks (0 = disabled)
	completedCount int // tasks completed since last retrain

	// OnTransition is called on every phase change. Set by sigild to trigger
	// LLM-generated suggestions on task completion, stuck detection, etc.
	OnTransition TransitionCallback
}

// NewTracker creates a Tracker backed by the given store.
func NewTracker(s store.ReadWriter, log *slog.Logger) *Tracker {
	return &Tracker{
		store: s,
		log:   log,
	}
}

// SetMLEngine configures the ML prediction backend.
func (t *Tracker) SetMLEngine(predictor MLPredictor, dbPath string, retrainEvery int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mlPredictor = predictor
	t.dbPath = dbPath
	t.retrainEvery = retrainEvery
}

// Restore loads the current task from the store so the tracker survives
// daemon restarts.
func (t *Tracker) Restore(ctx context.Context) error {
	rec, err := t.store.QueryCurrentTask(ctx)
	if err != nil {
		return fmt.Errorf("task tracker: restore: %w", err)
	}
	if rec == nil {
		return nil
	}
	t.current = recordToTask(rec)
	t.log.Info("task tracker: restored task", "id", t.current.ID, "phase", t.current.Phase, "repo", t.current.RepoRoot)
	return nil
}

// Current returns a snapshot of the current task.  Returns nil if idle.
func (t *Tracker) Current() *Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil
	}
	cp := *t.current
	files := make(map[string]int, len(t.current.FilesTouched))
	for k, v := range t.current.FilesTouched {
		files[k] = v
	}
	cp.FilesTouched = files
	return &cp
}

// RunEventLoop reads events from the broadcast channel and processes them
// until the channel is closed or ctx is cancelled.
func (t *Tracker) RunEventLoop(ctx context.Context, events <-chan event.Event) {
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return
			}
			t.Process(ctx, e)
		case <-ctx.Done():
			return
		}
	}
}

// Process handles a single event, updating the task state machine.
func (t *Tracker) Process(ctx context.Context, e event.Event) {
	sig, ok := ClassifyEvent(e)
	if !ok {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Reset idle timer on every relevant signal.
	t.resetIdleTimer(ctx)

	repo := RepoFromEvent(e)
	now := e.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	// If we have no current task and get a meaningful signal, start one.
	if t.current == nil {
		if sig == SignalIdleTimeout {
			return
		}
		t.startTask(ctx, repo, now)
	}

	// If the event is from a different repo, complete current and start new.
	if repo != "" && t.current.RepoRoot != "" && repo != t.current.RepoRoot {
		t.completeCurrentTask(ctx, now)
		t.startTask(ctx, repo, now)
	}

	// For terminal test commands, use the result signal instead.
	if sig == SignalTestCmd {
		t.current.TestRuns++
		sig = ClassifyTerminalResult(e)
	}

	// Track test results.
	if sig == SignalTestPass {
		t.current.TestFailures = 0
	}
	if sig == SignalTestFail {
		t.current.TestFailures++
	}

	// Run the state machine.
	oldPhase := t.current.Phase
	newPhase := Transition(t.current.Phase, sig)

	// Override: consecutive failures escalate verifying → stuck.
	if newPhase == PhaseVerifying && t.current.TestFailures >= stuckThreshold {
		newPhase = PhaseStuck
	}

	// ML-enhanced stuck prediction: if we're in verifying and ML predicts
	// stuck with high probability, escalate early (before heuristic threshold).
	if newPhase == PhaseVerifying && t.current.TestFailures > 0 && t.mlPredictor != nil && t.mlPredictor.Enabled() {
		stuckProb := t.predictStuck(ctx)
		if stuckProb > 0.7 {
			t.log.Info("ml: early stuck prediction", "probability", stuckProb)
			newPhase = PhaseStuck
		}
	}

	t.current.Phase = newPhase
	t.current.LastActivity = now

	// Track file edits.
	if sig == SignalFileEdit {
		if path, ok := e.Payload["path"].(string); ok && path != "" {
			t.current.FilesTouched[path]++
		}
	}

	// Track commits.
	if sig == SignalCommit {
		t.current.CommitCount++
	}

	// Detect branch from git HEAD changes.
	if sig == SignalBranchSwitch {
		if repo != "" {
			if branch := readBranch(repo); branch != "" {
				t.current.Branch = branch
			}
		}
	}

	// Handle task completion (completing → idle).
	if oldPhase != PhaseIdle && newPhase == PhaseIdle {
		t.completeCurrentTask(ctx, now)
		return
	}

	// Persist on phase change and fire callback.
	if oldPhase != newPhase {
		t.log.Info("task phase transition", "from", oldPhase, "to", newPhase, "repo", t.current.RepoRoot)
		t.persist(ctx)

		if t.OnTransition != nil {
			snapshot := *t.current
			go t.OnTransition(oldPhase, newPhase, &snapshot)
		}
	}
}

// startTask creates a new task and persists it.
func (t *Tracker) startTask(ctx context.Context, repo string, now time.Time) {
	id := fmt.Sprintf("t_%d", now.UnixMilli())
	branch := ""
	if repo != "" {
		branch = readBranch(repo)
	}

	t.current = &Task{
		ID:           id,
		RepoRoot:     repo,
		Branch:       branch,
		Phase:        PhaseIdle, // will transition immediately
		FilesTouched: make(map[string]int),
		StartedAt:    now,
		LastActivity: now,
	}

	t.persist(ctx)
	t.log.Info("task started", "id", id, "repo", repo, "branch", branch)
}

// completeCurrentTask marks the current task as completed and persists it.
// If ML is enabled and enough tasks have completed, triggers retraining.
func (t *Tracker) completeCurrentTask(ctx context.Context, now time.Time) {
	if t.current == nil {
		return
	}
	t.current.Phase = PhaseIdle
	t.current.CompletedAt = &now
	t.persist(ctx)
	t.log.Info("task completed", "id", t.current.ID, "commits", t.current.CommitCount, "files", len(t.current.FilesTouched))
	t.current = nil

	// Trigger ML retraining after N completed tasks.
	t.completedCount++
	if t.retrainEvery > 0 && t.mlPredictor != nil && t.mlPredictor.Enabled() && t.completedCount >= t.retrainEvery {
		t.completedCount = 0
		go func() {
			t.log.Info("ml: triggering retraining", "db", t.dbPath)
			if err := t.mlPredictor.Train(ctx, t.dbPath); err != nil {
				t.log.Warn("ml: retraining failed", "err", err)
			} else {
				t.log.Info("ml: retraining complete")
			}
		}()
	}
}

// persist writes the current task to the store.
func (t *Tracker) persist(ctx context.Context) {
	if t.current == nil {
		return
	}
	rec := taskToRecord(t.current)

	// Try update first; if no rows affected, insert.
	if err := t.store.UpdateTask(ctx, rec); err != nil {
		if err := t.store.InsertTask(ctx, rec); err != nil {
			t.log.Error("task tracker: persist", "err", err)
		}
	}
}

// predictStuck queries the ML engine for stuck probability based on current task state.
// Returns 0.0 on any error (fail-open: heuristics still apply).
func (t *Tracker) predictStuck(ctx context.Context) float64 {
	if t.current == nil || t.mlPredictor == nil {
		return 0
	}
	elapsed := time.Since(t.current.StartedAt).Seconds()
	totalEdits := 0
	for _, n := range t.current.FilesTouched {
		totalEdits += n
	}
	editVelocity := 0.0
	if elapsed > 0 {
		editVelocity = float64(totalEdits) / (elapsed / 60)
	}
	fileSwitchRate := 0.0
	if totalEdits > 0 {
		fileSwitchRate = float64(len(t.current.FilesTouched)) / float64(totalEdits)
	}

	features := map[string]any{
		"test_failure_count":        t.current.TestFailures,
		"time_in_phase_sec":         time.Since(t.current.LastActivity).Seconds(),
		"edit_velocity":             editVelocity,
		"file_switch_rate":          fileSwitchRate,
		"session_length_sec":        elapsed,
		"time_since_last_commit_sec": elapsed, // approximate
	}

	result, err := t.mlPredictor.Predict(ctx, "stuck", features)
	if err != nil {
		t.log.Debug("ml: stuck prediction failed", "err", err)
		return 0
	}
	if prob, ok := result["probability"].(float64); ok {
		return prob
	}
	return 0
}

// resetIdleTimer resets the idle timeout.  If no signal arrives within
// idleTimeout, an idle transition is triggered.
func (t *Tracker) resetIdleTimer(ctx context.Context) {
	if t.idleTimer != nil {
		t.idleTimer.Stop()
	}
	t.idleTimer = time.AfterFunc(idleTimeout, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.current != nil {
			t.log.Info("task idle timeout", "id", t.current.ID)
			t.completeCurrentTask(ctx, time.Now())
		}
	})
}

// readBranch reads the current branch from .git/HEAD in the given repo root.
// Returns empty string on any error or detached HEAD.
func readBranch(repoRoot string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(line, prefix) {
		return line[len(prefix):]
	}
	return "" // detached HEAD
}

// recordToTask converts a store.TaskRecord to a Task.
func recordToTask(rec *store.TaskRecord) *Task {
	return &Task{
		ID:           rec.ID,
		RepoRoot:     rec.RepoRoot,
		Branch:       rec.Branch,
		Phase:        Phase(rec.Phase),
		FilesTouched: rec.Files,
		StartedAt:    rec.StartedAt,
		LastActivity: rec.LastActivity,
		CompletedAt:  rec.CompletedAt,
		CommitCount:  rec.CommitCount,
		TestRuns:     rec.TestRuns,
		TestFailures: rec.TestFailures,
	}
}

// taskToRecord converts a Task to a store.TaskRecord.
func taskToRecord(t *Task) store.TaskRecord {
	return store.TaskRecord{
		ID:           t.ID,
		RepoRoot:     t.RepoRoot,
		Branch:       t.Branch,
		Phase:        string(t.Phase),
		Files:        t.FilesTouched,
		StartedAt:    t.StartedAt,
		LastActivity: t.LastActivity,
		CompletedAt:  t.CompletedAt,
		CommitCount:  t.CommitCount,
		TestRuns:     t.TestRuns,
		TestFailures: t.TestFailures,
	}
}
