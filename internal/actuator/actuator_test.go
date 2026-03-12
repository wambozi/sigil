package actuator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

type mockStore struct {
	actions []string
}

func (m *mockStore) InsertAction(_ context.Context, actionID, description, executeCmd, undoCmd string, createdAt, expiresAt time.Time) error {
	m.actions = append(m.actions, actionID)
	return nil
}

type testActuator struct {
	name    string
	actions []Action
}

func (t *testActuator) Name() string { return t.name }
func (t *testActuator) Check(_ context.Context) ([]Action, error) {
	return t.actions, nil
}

func TestRegistry_Notify(t *testing.T) {
	store := &mockStore{}
	var notified []Action
	reg := New(store, func(a Action) {
		notified = append(notified, a)
	}, nil)

	action := Action{
		ID:          "test-1",
		Description: "Test action",
		ExpiresAt:   time.Now().Add(30 * time.Second),
	}
	reg.Notify(action)

	if len(store.actions) != 1 || store.actions[0] != "test-1" {
		t.Errorf("expected store to have action test-1; got %v", store.actions)
	}
	if len(notified) != 1 || notified[0].ID != "test-1" {
		t.Errorf("expected notify callback to receive action test-1; got %v", notified)
	}
}

func TestRegistry_Register(t *testing.T) {
	reg := New(nil, nil, nil)
	ta := &testActuator{name: "test"}
	reg.Register(ta)

	if len(reg.actuators) != 1 {
		t.Errorf("expected 1 registered actuator; got %d", len(reg.actuators))
	}
}

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
			got := isTestOrBuildCmd(tt.cmd)
			if got != tt.want {
				t.Errorf("isTestOrBuildCmd(%q) = %v; want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestExitCodeFromPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    int
	}{
		{
			name:    "float64 zero",
			payload: map[string]any{"exit_code": float64(0)},
			want:    0,
		},
		{
			name:    "float64 non-zero",
			payload: map[string]any{"exit_code": float64(1)},
			want:    1,
		},
		{
			name:    "int",
			payload: map[string]any{"exit_code": int(2)},
			want:    2,
		},
		{
			name:    "int64",
			payload: map[string]any{"exit_code": int64(127)},
			want:    127,
		},
		{
			name:    "missing key returns -1",
			payload: map[string]any{},
			want:    -1,
		},
		{
			name:    "wrong type returns -1",
			payload: map[string]any{"exit_code": "zero"},
			want:    -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitCodeFromPayload(tt.payload)
			if got != tt.want {
				t.Errorf("exitCodeFromPayload(%v) = %d; want %d", tt.payload, got, tt.want)
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

func TestContainerWarmActuator_disabled(t *testing.T) {
	a := NewContainerWarmActuator(nil, nil, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("expected nil actions when disabled; got %v", actions)
	}
}

// mockTerminalQuerier implements TerminalQuerier for testing.
type mockTerminalQuerier struct {
	events []event.Event
	err    error
}

func (m *mockTerminalQuerier) QueryTerminalEvents(_ context.Context, _ time.Time) ([]event.Event, error) {
	return m.events, m.err
}

func TestContainerWarmActuator_noEvents(t *testing.T) {
	q := &mockTerminalQuerier{events: []event.Event{}}
	a := NewContainerWarmActuator(q, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("expected nil actions when no events; got %v", actions)
	}
}

func TestFindTypicalStartHour(t *testing.T) {
	a := &ContainerWarmActuator{}

	t.Run("empty events returns -1", func(t *testing.T) {
		got := a.findTypicalStartHour(nil)
		if got != -1 {
			t.Errorf("want -1; got %d", got)
		}
	})

	t.Run("single event is a session start", func(t *testing.T) {
		events := []event.Event{
			{Timestamp: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)},
		}
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})

	t.Run("gap under 2h is not a new session", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
		events := []event.Event{
			{Timestamp: base},
			{Timestamp: base.Add(30 * time.Minute)},
			{Timestamp: base.Add(90 * time.Minute)},
		}
		// Only the first event counts as a session start (hour 9).
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})

	t.Run("2h+ gap triggers new session start", func(t *testing.T) {
		// Three sessions at hour 9, one session at hour 14 — hour 9 should win.
		events := []event.Event{
			// Day 1: session at 09:00, then gap, session at 14:00
			{Timestamp: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)},
			{Timestamp: time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)},
			// Day 2: session at 09:00
			{Timestamp: time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)},
			// Day 3: session at 09:00
			{Timestamp: time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)},
		}
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})
}

func TestFindComposeServices(t *testing.T) {
	dir := t.TempDir()

	content := `version: "3"
services:
  web:
    image: nginx
  db:
    image: postgres
other:
  key: value
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 2 {
		t.Fatalf("expected 2 services; got %d: %v", len(services), services)
	}

	found := make(map[string]bool)
	for _, s := range services {
		found[s] = true
	}
	for _, want := range []string{"web", "db"} {
		if !found[want] {
			t.Errorf("expected service %q in results; got %v", want, services)
		}
	}
}

// errStore always returns an error from InsertAction.
type errStore struct{}

func (e *errStore) InsertAction(_ context.Context, _, _, _, _ string, _, _ time.Time) error {
	return errors.New("insert failed")
}

// errActuator always returns an error from Check.
type errActuator struct{}

func (e *errActuator) Name() string { return "err-actuator" }
func (e *errActuator) Check(_ context.Context) ([]Action, error) {
	return nil, errors.New("check failed")
}

func TestRegistry_poll_withActions(t *testing.T) {
	store := &mockStore{}
	var notified []Action

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := New(store, func(a Action) {
		notified = append(notified, a)
	}, log)

	reg.Register(&testActuator{
		name: "info-action",
		actions: []Action{
			{
				ID:          "action-1",
				Description: "informational — no shell exec",
				ExecuteCmd:  "", // empty: poll skips exec and goes straight to store+notify
				ExpiresAt:   time.Now().Add(30 * time.Second),
			},
		},
	})

	reg.poll(context.Background())

	if len(store.actions) != 1 || store.actions[0] != "action-1" {
		t.Errorf("expected store to contain action-1; got %v", store.actions)
	}
	if len(notified) != 1 || notified[0].ID != "action-1" {
		t.Errorf("expected notify to receive action-1; got %v", notified)
	}
}

func TestRegistry_poll_storeError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// errStore returns an error; poll must log and continue without panicking.
	reg := New(&errStore{}, nil, log)
	reg.Register(&testActuator{
		name: "store-err",
		actions: []Action{
			{
				ID:        "store-err-1",
				ExpiresAt: time.Now().Add(30 * time.Second),
			},
		},
	})

	reg.poll(context.Background()) // must not panic
}

func TestRegistry_poll_actuatorError(t *testing.T) {
	store := &mockStore{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := New(store, nil, log)
	reg.Register(&errActuator{})

	// Must not panic; error is logged and the actuator is skipped.
	reg.poll(context.Background())

	if len(store.actions) != 0 {
		t.Errorf("expected no actions stored on actuator error; got %v", store.actions)
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

func TestContainerWarmActuator_Name(t *testing.T) {
	a := NewContainerWarmActuator(nil, nil, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := a.Name(); got != "container_warm" {
		t.Errorf("Name() = %q; want %q", got, "container_warm")
	}
}

func TestContainerWarmActuator_storeError(t *testing.T) {
	q := &mockTerminalQuerier{err: errors.New("db unavailable")}
	a := NewContainerWarmActuator(q, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := a.Check(context.Background())
	if err == nil {
		t.Fatal("expected error when store returns error; got nil")
	}
	if !errors.Is(err, q.err) {
		t.Errorf("error chain does not wrap store error: %v", err)
	}
}

func TestRegistry_Run_cancelImmediate(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := New(nil, nil, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run is called so the select fires immediately

	done := make(chan struct{})
	go func() {
		reg.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Run returned after context cancellation — correct.
	case <-time.After(2 * time.Second):
		t.Fatal("Registry.Run did not return after context cancellation")
	}
}

func TestRegistry_poll_execFailure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &mockStore{}
	var notified []Action

	reg := New(store, func(a Action) {
		notified = append(notified, a)
	}, log)

	// ExecuteCmd is a command guaranteed to fail; poll should log and skip it.
	reg.Register(&testActuator{
		name: "exec-fail",
		actions: []Action{
			{
				ID:         "exec-fail-1",
				ExecuteCmd: "false", // exits with code 1 on any POSIX system
				ExpiresAt:  time.Now().Add(30 * time.Second),
			},
		},
	})

	// Must not panic; the action is skipped after the exec error.
	reg.poll(context.Background())

	if len(store.actions) != 0 {
		t.Errorf("action should not be persisted after exec failure; got %v", store.actions)
	}
	if len(notified) != 0 {
		t.Errorf("notify should not be called after exec failure; got %v", notified)
	}
}

// Ensure the fmt import is used (it is referenced in TestExitCodeFromPayload via
// t.Errorf format strings, but the compiler needs a direct use of the package).
// This blank import assertion keeps "fmt" live without dummy calls.
var _ = fmt.Sprintf
