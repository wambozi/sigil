package actuator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// mockStore records action IDs passed to InsertAction.
type mockStore struct {
	actions []string
}

func (m *mockStore) InsertAction(_ context.Context, actionID, _, _, _ string, _, _ time.Time) error {
	m.actions = append(m.actions, actionID)
	return nil
}

// errStore always returns an error from InsertAction.
type errStore struct{}

func (e *errStore) InsertAction(_ context.Context, _, _, _, _ string, _, _ time.Time) error {
	return errors.New("insert failed")
}

// testActuator is a configurable stub Actuator.
type testActuator struct {
	name    string
	actions []Action
}

func (t *testActuator) Name() string { return t.name }
func (t *testActuator) Check(_ context.Context) ([]Action, error) {
	return t.actions, nil
}

// errActuator always returns an error from Check.
type errActuator struct{}

func (e *errActuator) Name() string { return "err-actuator" }
func (e *errActuator) Check(_ context.Context) ([]Action, error) {
	return nil, errors.New("check failed")
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

func TestRegistry_SetRunCmd(t *testing.T) {
	reg := New(nil, nil, nil)
	var called bool
	reg.SetRunCmd(func(_ context.Context, _ string) error {
		called = true
		return nil
	})
	_ = reg.runCmd(context.Background(), "anything")
	if !called {
		t.Error("SetRunCmd did not replace the runCmd function")
	}
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

func TestRegistry_poll_execFailure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &mockStore{}
	var notified []Action

	reg := New(store, func(a Action) {
		notified = append(notified, a)
	}, log)
	reg.SetRunCmd(func(_ context.Context, _ string) error {
		return errors.New("exec failed")
	})

	reg.Register(&testActuator{
		name: "exec-fail",
		actions: []Action{
			{
				ID:         "exec-fail-1",
				ExecuteCmd: "echo should-not-run",
				ExpiresAt:  time.Now().Add(30 * time.Second),
			},
		},
	})

	reg.poll(context.Background())

	if len(store.actions) != 0 {
		t.Errorf("action should not be persisted after exec failure; got %v", store.actions)
	}
	if len(notified) != 0 {
		t.Errorf("notify should not be called after exec failure; got %v", notified)
	}
}

func TestRegistry_poll_execSuccess(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &mockStore{}

	var executed []string
	reg := New(store, nil, log)
	reg.SetRunCmd(func(_ context.Context, cmd string) error {
		executed = append(executed, cmd)
		return nil
	})

	reg.Register(&testActuator{
		name: "exec-ok",
		actions: []Action{
			{
				ID:         "exec-ok-1",
				ExecuteCmd: "docker compose up -d",
				ExpiresAt:  time.Now().Add(30 * time.Second),
			},
		},
	})

	reg.poll(context.Background())

	if len(executed) != 1 || executed[0] != "docker compose up -d" {
		t.Errorf("expected RunCmd to receive command; got %v", executed)
	}
	if len(store.actions) != 1 || store.actions[0] != "exec-ok-1" {
		t.Errorf("expected action persisted after successful exec; got %v", store.actions)
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

func TestRegistry_Run_tickerFires(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &mockStore{}

	reg := New(store, nil, log)
	reg.Register(&testActuator{
		name: "tick-counter",
		actions: []Action{
			{ID: "tick-1", ExpiresAt: time.Now().Add(30 * time.Second)},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Run polls once immediately on start. Cancel right after so we don't wait
	// for the 30-second ticker. We verify the goroutine terminates and the
	// initial poll ran.
	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.Run(ctx)
	}()

	cancel()
	select {
	case <-done:
		// Good.
	case <-time.After(2 * time.Second):
		t.Fatal("Registry.Run did not exit after cancel")
	}

	// At minimum the initial poll ran once.
	if len(store.actions) < 1 {
		t.Errorf("expected at least 1 store insertion from initial poll; got %d", len(store.actions))
	}
}

func TestDefaultRunCmd_success(t *testing.T) {
	err := defaultRunCmd(context.Background(), "true")
	if err != nil {
		t.Errorf("defaultRunCmd(true) = %v; want nil", err)
	}
}

func TestDefaultRunCmd_failure(t *testing.T) {
	err := defaultRunCmd(context.Background(), "false")
	if err == nil {
		t.Error("defaultRunCmd(false) = nil; want non-nil error")
	}
}

func TestDefaultRunCmd_cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := defaultRunCmd(ctx, "sleep 60")
	if err == nil {
		t.Error("expected error for cancelled context; got nil")
	}
}
