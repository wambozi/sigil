package main

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestDrainWithTimeout_completesBeforeDeadline(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	called := false
	drainWithTimeoutAndExit(log, time.Second, func() {
		called = true
	}, func(code int) {
		t.Fatalf("exitFn called with code %d — should not have timed out", code)
	})

	if !called {
		t.Error("drainFn was never called")
	}
}

func TestDrainWithTimeout_slowDrainTriggersExit(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	exitCalled := make(chan int, 1)
	drainWithTimeoutAndExit(log, 50*time.Millisecond, func() {
		time.Sleep(500 * time.Millisecond) // deliberately slow
	}, func(code int) {
		exitCalled <- code
	})

	select {
	case code := <-exitCalled:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	case <-time.After(time.Second):
		t.Error("exitFn was never called — timeout did not fire")
	}
}
