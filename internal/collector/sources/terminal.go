package sources

import (
	"context"

	"github.com/wambozi/sigil/internal/event"
)

// TerminalSource receives shell command events pushed from the socket "ingest"
// handler.  Unlike the other sources it has no background goroutine of its own
// — callers push events via Ingest and Events() forwards them to the collector.
type TerminalSource struct {
	in chan event.Event
}

// NewTerminalSource creates a TerminalSource with a 256-event buffer.
func NewTerminalSource() *TerminalSource {
	return &TerminalSource{in: make(chan event.Event, 256)}
}

func (s *TerminalSource) Name() string { return "terminal" }

// Ingest pushes an event into the buffer.  Non-blocking: events are dropped if
// the buffer is full (the shell hook is fire-and-forget; backpressure would
// hang the user's prompt).
func (s *TerminalSource) Ingest(e event.Event) {
	select {
	case s.in <- e:
	default:
	}
}

// Events forwards buffered terminal events to the collector until ctx is
// cancelled.
func (s *TerminalSource) Events(ctx context.Context) (<-chan event.Event, error) {
	out := make(chan event.Event, 64)

	go func() {
		defer close(out)
		for {
			select {
			case e := <-s.in:
				select {
				case out <- e:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
