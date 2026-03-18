package task

import (
	"testing"

	"github.com/wambozi/sigil/internal/event"
)

// ---------------------------------------------------------------------------
// Transition tests
// ---------------------------------------------------------------------------

func TestTransition(t *testing.T) {
	tests := []struct {
		name string
		from Phase
		sig  Signal
		want Phase
	}{
		// idle
		{"idle+FileEdit→editing", PhaseIdle, SignalFileEdit, PhaseEditing},
		{"idle+BranchSwitch→transitioning", PhaseIdle, SignalBranchSwitch, PhaseTransitioning},

		// editing
		{"editing+TestCmd→verifying", PhaseEditing, SignalTestCmd, PhaseVerifying},
		{"editing+Staging→completing", PhaseEditing, SignalStaging, PhaseCompleting},
		{"editing+Commit→completing", PhaseEditing, SignalCommit, PhaseCompleting},
		{"editing+BranchSwitch→transitioning", PhaseEditing, SignalBranchSwitch, PhaseTransitioning},

		// verifying
		{"verifying+TestPass→completing", PhaseVerifying, SignalTestPass, PhaseCompleting},
		{"verifying+TestFail→verifying", PhaseVerifying, SignalTestFail, PhaseVerifying},
		{"verifying+FileEdit→editing", PhaseVerifying, SignalFileEdit, PhaseEditing},
		{"verifying+Commit→completing", PhaseVerifying, SignalCommit, PhaseCompleting},

		// stuck
		{"stuck+FileEdit→editing", PhaseStuck, SignalFileEdit, PhaseEditing},
		{"stuck+BranchSwitch→transitioning", PhaseStuck, SignalBranchSwitch, PhaseTransitioning},

		// completing
		{"completing+Commit→idle", PhaseCompleting, SignalCommit, PhaseIdle},
		{"completing+FileEdit→editing", PhaseCompleting, SignalFileEdit, PhaseEditing},
		{"completing+BranchSwitch→transitioning", PhaseCompleting, SignalBranchSwitch, PhaseTransitioning},

		// transitioning
		{"transitioning+FileEdit→editing", PhaseTransitioning, SignalFileEdit, PhaseEditing},

		// IdleTimeout from every phase
		{"idle+IdleTimeout→idle", PhaseIdle, SignalIdleTimeout, PhaseIdle},
		{"editing+IdleTimeout→idle", PhaseEditing, SignalIdleTimeout, PhaseIdle},
		{"verifying+IdleTimeout→idle", PhaseVerifying, SignalIdleTimeout, PhaseIdle},
		{"stuck+IdleTimeout→idle", PhaseStuck, SignalIdleTimeout, PhaseIdle},
		{"completing+IdleTimeout→idle", PhaseCompleting, SignalIdleTimeout, PhaseIdle},
		{"transitioning+IdleTimeout→idle", PhaseTransitioning, SignalIdleTimeout, PhaseIdle},

		// Unknown / no-op transitions — phase should not change.
		{"idle+TestPass→idle", PhaseIdle, SignalTestPass, PhaseIdle},
		{"editing+TestPass→editing", PhaseEditing, SignalTestPass, PhaseEditing},
		{"stuck+Commit→stuck", PhaseStuck, SignalCommit, PhaseStuck},
		{"transitioning+Commit→transitioning", PhaseTransitioning, SignalCommit, PhaseTransitioning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Transition(tt.from, tt.sig)
			if got != tt.want {
				t.Errorf("Transition(%q, %d) = %q; want %q", tt.from, tt.sig, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ClassifyEvent tests
// ---------------------------------------------------------------------------

func TestClassifyEvent(t *testing.T) {
	tests := []struct {
		name   string
		event  event.Event
		want   Signal
		wantOK bool
	}{
		{
			name:   "file event → FileEdit",
			event:  event.Event{Kind: event.KindFile, Payload: map[string]any{"path": "/src/main.go"}},
			want:   SignalFileEdit,
			wantOK: true,
		},
		{
			name: "terminal test cmd → TestCmd",
			event: event.Event{
				Kind:    event.KindTerminal,
				Payload: map[string]any{"cmd": "go test ./...", "exit_code": float64(0)},
			},
			want:   SignalTestCmd,
			wantOK: true,
		},
		{
			name: "terminal non-test cmd → false",
			event: event.Event{
				Kind:    event.KindTerminal,
				Payload: map[string]any{"cmd": "ls -la", "exit_code": float64(0)},
			},
			want:   0,
			wantOK: false,
		},
		{
			name: "git commit → Commit",
			event: event.Event{
				Kind:    event.KindGit,
				Payload: map[string]any{"git_kind": "commit"},
			},
			want:   SignalCommit,
			wantOK: true,
		},
		{
			name: "git head_change → BranchSwitch",
			event: event.Event{
				Kind:    event.KindGit,
				Payload: map[string]any{"git_kind": "head_change"},
			},
			want:   SignalBranchSwitch,
			wantOK: true,
		},
		{
			name: "git index_change → Staging",
			event: event.Event{
				Kind:    event.KindGit,
				Payload: map[string]any{"git_kind": "index_change"},
			},
			want:   SignalStaging,
			wantOK: true,
		},
		{
			name: "git unknown kind → false",
			event: event.Event{
				Kind:    event.KindGit,
				Payload: map[string]any{"git_kind": "fetch"},
			},
			want:   0,
			wantOK: false,
		},
		{
			name:   "process event → false",
			event:  event.Event{Kind: event.KindProcess, Payload: map[string]any{}},
			want:   0,
			wantOK: false,
		},
		{
			name:   "hyprland event → false",
			event:  event.Event{Kind: event.KindHyprland, Payload: map[string]any{}},
			want:   0,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyEvent(tt.event)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ClassifyEvent() = (%d, %v); want (%d, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ClassifyTerminalResult tests
// ---------------------------------------------------------------------------

func TestClassifyTerminalResult(t *testing.T) {
	tests := []struct {
		name  string
		event event.Event
		want  Signal
	}{
		{
			name: "exit 0 → TestPass",
			event: event.Event{
				Kind:    event.KindTerminal,
				Payload: map[string]any{"cmd": "go test ./...", "exit_code": float64(0)},
			},
			want: SignalTestPass,
		},
		{
			name: "exit 1 → TestFail",
			event: event.Event{
				Kind:    event.KindTerminal,
				Payload: map[string]any{"cmd": "go test ./...", "exit_code": float64(1)},
			},
			want: SignalTestFail,
		},
		{
			name: "missing exit code → TestFail (conservative)",
			event: event.Event{
				Kind:    event.KindTerminal,
				Payload: map[string]any{"cmd": "go test ./..."},
			},
			want: SignalTestFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTerminalResult(tt.event)
			if got != tt.want {
				t.Errorf("ClassifyTerminalResult() = %d; want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RepoFromEvent tests
// ---------------------------------------------------------------------------

func TestRepoFromEvent_git(t *testing.T) {
	e := event.Event{
		Kind:    event.KindGit,
		Payload: map[string]any{"repo_root": "/home/dev/myrepo", "git_kind": "commit"},
	}
	got := RepoFromEvent(e)
	if got != "/home/dev/myrepo" {
		t.Errorf("RepoFromEvent(git) = %q; want %q", got, "/home/dev/myrepo")
	}
}

func TestRepoFromEvent_gitMissing(t *testing.T) {
	e := event.Event{
		Kind:    event.KindGit,
		Payload: map[string]any{"git_kind": "commit"},
	}
	got := RepoFromEvent(e)
	if got != "" {
		t.Errorf("RepoFromEvent(git no repo_root) = %q; want empty", got)
	}
}

func TestRepoFromEvent_unknownKind(t *testing.T) {
	e := event.Event{
		Kind:    event.KindProcess,
		Payload: map[string]any{},
	}
	got := RepoFromEvent(e)
	if got != "" {
		t.Errorf("RepoFromEvent(process) = %q; want empty", got)
	}
}
