package task

import (
	"os"
	"path/filepath"

	"github.com/wambozi/sigil/internal/event"
)

// Signal represents a meaningful developer-intent signal extracted from a raw
// event.  Signals drive the task state machine.
type Signal int

const (
	SignalFileEdit     Signal = iota + 1 // a file was modified
	SignalTestCmd                        // a test/build command was invoked
	SignalTestPass                       // a test/build command exited 0
	SignalTestFail                       // a test/build command exited non-zero
	SignalCommit                         // a git commit was created
	SignalBranchSwitch                   // HEAD changed to a different branch
	SignalStaging                        // files were staged (git add)
	SignalIdleTimeout                    // no activity for the configured window
)

// ClassifyEvent extracts a task-lifecycle Signal from a raw event.
// The second return value is false when the event carries no signal
// relevant to task tracking (e.g. process polling, window focus).
func ClassifyEvent(e event.Event) (Signal, bool) {
	switch e.Kind {
	case event.KindFile:
		return SignalFileEdit, true

	case event.KindTerminal:
		cmd := event.CmdFromPayload(e.Payload)
		if !event.IsTestOrBuildCmd(cmd) {
			return 0, false
		}
		return SignalTestCmd, true

	case event.KindGit:
		gitKind, _ := e.Payload["git_kind"].(string)
		switch gitKind {
		case "commit":
			return SignalCommit, true
		case "head_change":
			return SignalBranchSwitch, true
		case "index_change":
			return SignalStaging, true
		}
		return 0, false

	default:
		return 0, false
	}
}

// ClassifyTerminalResult inspects the exit code of a completed terminal event
// that has already been classified as a test command.  It returns
// SignalTestPass or SignalTestFail.  If the exit code is absent it falls back
// to SignalTestFail (conservative).
func ClassifyTerminalResult(e event.Event) Signal {
	code, ok := event.ExitCodeFromPayload(e.Payload)
	if ok && code == 0 {
		return SignalTestPass
	}
	return SignalTestFail
}

// RepoFromEvent attempts to determine the repository root associated with an
// event.  For git events it reads the "repo_root" payload field.  For file
// events it walks up the directory tree looking for a .git directory.
// Returns an empty string if no repo can be determined.
func RepoFromEvent(e event.Event) string {
	// Git events carry the repo root explicitly.
	if e.Kind == event.KindGit {
		if rr, ok := e.Payload["repo_root"].(string); ok && rr != "" {
			return rr
		}
	}

	// File events: infer from the file path.
	if e.Kind == event.KindFile {
		p, _ := e.Payload["path"].(string)
		if p == "" {
			return ""
		}
		return findRepoRoot(p)
	}

	return ""
}

// findRepoRoot walks from the given path upward until it finds a directory
// containing .git, or reaches the filesystem root.
func findRepoRoot(path string) string {
	dir := filepath.Dir(path)
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached root
		}
		dir = parent
	}
}
