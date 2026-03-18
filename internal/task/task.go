// Package task implements lifecycle tracking for developer work sessions.
// A task progresses through phases (idle → editing → verifying → completing)
// driven by signals extracted from raw events.  The state machine is a pure
// function with no side effects — the tracker (Stage 2) will own mutation.
package task

import "time"

// Phase represents the current stage of a developer task.
type Phase string

const (
	PhaseIdle          Phase = "idle"
	PhaseEditing       Phase = "editing"
	PhaseVerifying     Phase = "verifying"
	PhaseStuck         Phase = "stuck"
	PhaseCompleting    Phase = "completing"
	PhaseTransitioning Phase = "transitioning"
)

// Task holds the accumulated state of a single logical unit of work.
type Task struct {
	ID           string         // unique task identifier
	RepoRoot     string         // absolute path to the repository root
	Branch       string         // git branch associated with this task
	Phase        Phase          // current lifecycle phase
	FilesTouched map[string]int // path → edit count
	StartedAt    time.Time      // when the task began
	LastActivity time.Time      // most recent signal timestamp
	CompletedAt  *time.Time     // set when the task reaches idle after completing
	CommitCount  int            // total commits observed
	TestRuns     int            // total test invocations
	TestFailures int            // consecutive test failures (reset on pass)
}

// Transition is a pure function that returns the next phase given the current
// phase and an incoming signal.  It encodes the full state machine but does
// NOT handle the "3 consecutive failures → stuck" rule — that is the
// tracker's responsibility (it can override the returned phase).
func Transition(current Phase, sig Signal) Phase {
	switch current {
	case PhaseIdle:
		switch sig {
		case SignalFileEdit:
			return PhaseEditing
		case SignalBranchSwitch:
			return PhaseTransitioning
		}

	case PhaseEditing:
		switch sig {
		case SignalTestCmd:
			return PhaseVerifying
		case SignalStaging:
			return PhaseCompleting
		case SignalCommit:
			return PhaseCompleting
		case SignalBranchSwitch:
			return PhaseTransitioning
		}

	case PhaseVerifying:
		switch sig {
		case SignalTestPass:
			return PhaseCompleting
		case SignalTestFail:
			return PhaseVerifying // stays; tracker may override to stuck
		case SignalFileEdit:
			return PhaseEditing
		case SignalCommit:
			return PhaseCompleting
		}

	case PhaseStuck:
		switch sig {
		case SignalFileEdit:
			return PhaseEditing
		case SignalBranchSwitch:
			return PhaseTransitioning
		}

	case PhaseCompleting:
		switch sig {
		case SignalCommit:
			return PhaseIdle
		case SignalFileEdit:
			return PhaseEditing
		case SignalBranchSwitch:
			return PhaseTransitioning
		}

	case PhaseTransitioning:
		switch sig {
		case SignalFileEdit:
			return PhaseEditing
		}
	}

	// IdleTimeout resets any phase to idle.
	if sig == SignalIdleTimeout {
		return PhaseIdle
	}

	// Unknown signal or no matching transition — stay put.
	return current
}
