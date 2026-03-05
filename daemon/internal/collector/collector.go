// Package collector orchestrates all observation sources and fans their
// event streams into the store.  Each source runs in its own goroutine.
// The collector itself is lifecycle-managed via Start/Stop.
package collector

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/store"
)

// Source is implemented by every event-producing subsystem.
type Source interface {
	// Name returns a stable identifier used in event.Source.
	Name() string

	// Events starts the source and returns a channel of observations.
	// The channel is closed when ctx is cancelled or the source terminates.
	Events(ctx context.Context) (<-chan event.Event, error)
}

// Collector fans in events from all registered sources and writes them to the
// store.  It also exposes a broadcast channel for in-process consumers (e.g.
// the analyzer's reactive tier).
type Collector struct {
	store   *store.Store
	sources []Source
	log     *slog.Logger

	// Broadcast receives every event that was successfully stored.
	// Consumers must read promptly — a slow consumer drops events.
	Broadcast chan event.Event

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a Collector.  sources may be extended before calling Start.
func New(s *store.Store, log *slog.Logger) *Collector {
	return &Collector{
		store:     s,
		log:       log,
		Broadcast: make(chan event.Event, 256),
	}
}

// Add registers an additional source.  Must be called before Start.
func (c *Collector) Add(src Source) {
	c.sources = append(c.sources, src)
}

// Start launches all sources and begins fanning their events into the store.
// It returns immediately; collection runs in the background until Stop is
// called or the parent context is cancelled.
func (c *Collector) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	for _, src := range c.sources {
		ch, err := src.Events(ctx)
		if err != nil {
			c.cancel()
			return err
		}

		c.wg.Add(1)
		go c.drain(ctx, src.Name(), ch)
	}

	return nil
}

// Stop signals all sources to terminate and waits for them to finish.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	close(c.Broadcast)
}

// drain reads events from a single source channel and writes them to the store.
func (c *Collector) drain(ctx context.Context, name string, ch <-chan event.Event) {
	defer c.wg.Done()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := c.store.InsertEvent(ctx, e); err != nil {
				c.log.Error("store event", "source", name, "err", err)
				continue
			}
			// Non-blocking broadcast — slow consumers drop events.
			select {
			case c.Broadcast <- e:
			default:
			}

		case <-ctx.Done():
			return
		}
	}
}
