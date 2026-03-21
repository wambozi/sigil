package actuator

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

func TestIsTestOrBuildCmd(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "empty string", cmd: "", want: false},
		{name: "go test", cmd: "go test ./...", want: true},
		{name: "go build", cmd: "go build ./cmd/sigild/", want: true},
		{name: "go vet", cmd: "go vet ./...", want: true},
		{name: "make", cmd: "make check", want: true},
		{name: "make bare", cmd: "make", want: true},
		{name: "cargo test", cmd: "cargo test --release", want: true},
		{name: "cargo build", cmd: "cargo build", want: true},
		{name: "npm test", cmd: "npm test", want: true},
		{name: "npm run test", cmd: "npm run test", want: true},
		{name: "npm run build", cmd: "npm run build", want: true},
		{name: "pytest", cmd: "pytest -v", want: true},
		{name: "python -m pytest", cmd: "python -m pytest tests/", want: true},
		{name: "gradlew", cmd: "./gradlew build", want: true},
		{name: "mvn test", cmd: "mvn test", want: true},
		{name: "mvn build", cmd: "mvn build", want: true},
		{name: "uppercase normalised", cmd: "GO TEST ./...", want: true},
		{name: "leading whitespace", cmd: "  go test ./...", want: true},
		{name: "ls is not a build cmd", cmd: "ls -la", want: false},
		{name: "git commit is not a build cmd", cmd: "git commit -m 'fix'", want: false},
		{name: "echo is not a build cmd", cmd: "echo hello", want: false},
		{name: "go run is not a build cmd", cmd: "go run main.go", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := event.IsTestOrBuildCmd(tt.cmd)
			if got != tt.want {
				t.Errorf("IsTestOrBuildCmd(%q) = %v; want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestExitCodeFromPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    int
		wantOK  bool
	}{
		{
			name:    "float64 zero",
			payload: map[string]any{"exit_code": float64(0)},
			want:    0,
			wantOK:  true,
		},
		{
			name:    "float64 non-zero",
			payload: map[string]any{"exit_code": float64(1)},
			want:    1,
			wantOK:  true,
		},
		{
			name:    "int",
			payload: map[string]any{"exit_code": int(2)},
			want:    2,
			wantOK:  true,
		},
		{
			name:    "int64",
			payload: map[string]any{"exit_code": int64(127)},
			want:    127,
			wantOK:  true,
		},
		{
			name:    "missing key",
			payload: map[string]any{},
			want:    0,
			wantOK:  false,
		},
		{
			name:    "wrong type",
			payload: map[string]any{"exit_code": "zero"},
			want:    0,
			wantOK:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := event.ExitCodeFromPayload(tt.payload)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ExitCodeFromPayload(%v) = (%d, %v); want (%d, %v)",
					tt.payload, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestBuildSplitActuator_RunEventLoop(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBuildSplitActuator(log)

	ch := make(chan event.Event, 4)

	// First terminal event with a build cmd and no exit_code: should trigger split-pane.
	ch <- event.Event{
		Kind:      event.KindTerminal,
		Payload:   map[string]any{"cmd": "go test ./..."},
		Timestamp: time.Now(),
	}
	// Second terminal event with a build cmd and exit_code=0: should trigger close-split.
	ch <- event.Event{
		Kind:      event.KindTerminal,
		Payload:   map[string]any{"cmd": "go test ./...", "exit_code": float64(0)},
		Timestamp: time.Now(),
	}
	close(ch)

	type call struct {
		typ string
	}
	var calls []call
	b.RunEventLoop(ch, func(action Action, typ string) {
		calls = append(calls, call{typ: typ})
	})

	if len(calls) != 2 {
		t.Fatalf("expected 2 splitNotify calls; got %d", len(calls))
	}
	if calls[0].typ != "split-pane" {
		t.Errorf("first call: want type %q; got %q", "split-pane", calls[0].typ)
	}
	if calls[1].typ != "close-split" {
		t.Errorf("second call: want type %q; got %q", "close-split", calls[1].typ)
	}
}

func TestBuildSplitActuator_ignoresNonTerminal(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBuildSplitActuator(log)

	ch := make(chan event.Event, 3)
	ch <- event.Event{Kind: event.KindFile, Payload: map[string]any{"path": "/tmp/foo.go"}, Timestamp: time.Now()}
	ch <- event.Event{Kind: event.KindGit, Payload: map[string]any{"ref": "main"}, Timestamp: time.Now()}
	ch <- event.Event{Kind: event.KindProcess, Payload: map[string]any{"pid": 42}, Timestamp: time.Now()}
	close(ch)

	called := false
	b.RunEventLoop(ch, func(_ Action, _ string) {
		called = true
	})

	if called {
		t.Error("splitNotify should not have been called for non-terminal events")
	}
}

func TestBuildSplitActuator_ignoresEmptyCmd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBuildSplitActuator(log)

	ch := make(chan event.Event, 1)
	// Terminal event with no "cmd" key — RunEventLoop must skip it silently.
	ch <- event.Event{
		Kind:      event.KindTerminal,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	}
	close(ch)

	called := false
	b.RunEventLoop(ch, func(_ Action, _ string) {
		called = true
	})

	if called {
		t.Error("splitNotify should not be called when cmd is empty")
	}
}

// TestBuildSplitActuator_exitCodeWithNoPendingBuild covers the third branch in
// RunEventLoop: a build command arrives with an exit code while pendingBuild is
// false, so the else-if !pendingBuild arm fires and emits a split-pane action.
func TestBuildSplitActuator_exitCodeWithNoPendingBuild(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBuildSplitActuator(log)

	// b.pendingBuild is false. Send a terminal event for a build command that
	// already has an exit code. exitCode will be 0, not -1, so the first
	// if-branch is skipped, the else-if pendingBuild branch is skipped, and
	// the else-if !pendingBuild branch fires.
	ch := make(chan event.Event, 1)
	ch <- event.Event{
		Kind:      event.KindTerminal,
		Payload:   map[string]any{"cmd": "go test ./...", "exit_code": float64(0)},
		Timestamp: time.Now(),
	}
	close(ch)

	type call struct{ typ string }
	var calls []call
	b.RunEventLoop(ch, func(action Action, typ string) {
		calls = append(calls, call{typ: typ})
	})

	if len(calls) != 1 {
		t.Fatalf("expected 1 splitNotify call; got %d", len(calls))
	}
	if calls[0].typ != "split-pane" {
		t.Errorf("expected split-pane; got %q", calls[0].typ)
	}
}
