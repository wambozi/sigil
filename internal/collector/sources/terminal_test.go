package sources

import (
	"context"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

func makeTerminalEvent(cmd string) event.Event {
	return event.Event{
		Kind:   event.KindTerminal,
		Source: "terminal",
		Payload: map[string]any{
			"cmd":       cmd,
			"exit_code": 0,
			"cwd":       "/home/nick/code",
		},
		Timestamp: time.Now(),
	}
}

func TestTerminalSource_IngestAndReceive(t *testing.T) {
	src := NewTerminalSource()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	e := makeTerminalEvent("go build ./...")
	src.Ingest(e)

	select {
	case got := <-ch:
		if got.Kind != event.KindTerminal {
			t.Errorf("Kind: got %q, want %q", got.Kind, event.KindTerminal)
		}
		if got.Payload["cmd"] != "go build ./..." {
			t.Errorf("cmd payload: got %v", got.Payload["cmd"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingested event")
	}
}

func TestTerminalSource_MultipleEvents(t *testing.T) {
	src := NewTerminalSource()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	cmds := []string{"go test ./...", "git status", "make build"}
	for _, cmd := range cmds {
		src.Ingest(makeTerminalEvent(cmd))
	}

	for i, want := range cmds {
		select {
		case got := <-ch:
			if got.Payload["cmd"] != want {
				t.Errorf("event %d: got cmd %q, want %q", i, got.Payload["cmd"], want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestTerminalSource_DropsWhenBufferFull(t *testing.T) {
	src := NewTerminalSource()
	// Do not start Events() — buffer will fill and Ingest must not block.

	// Fill the entire 256-slot internal buffer.
	for i := 0; i < 256; i++ {
		src.Ingest(makeTerminalEvent("go test ./..."))
	}

	// This one should be dropped (non-blocking), not hang.
	done := make(chan struct{})
	go func() {
		src.Ingest(makeTerminalEvent("dropped"))
		close(done)
	}()

	select {
	case <-done:
		// Good — Ingest returned immediately.
	case <-time.After(time.Second):
		t.Fatal("Ingest blocked on full buffer")
	}
}

func TestTerminalSource_ContextCancelClosesChannel(t *testing.T) {
	src := NewTerminalSource()
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := src.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	cancel()

	// Channel must close after cancellation. We may need to drain one in-flight
	// event first, so loop until closed or timeout.
	timeout := time.After(time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return // channel closed as expected
			}
		case <-timeout:
			t.Fatal("channel not closed after context cancel")
		}
	}
}
