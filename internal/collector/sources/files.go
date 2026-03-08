// Package sources contains individual collector source implementations.
// Each source implements the collector.Source interface and emits events
// over a channel for as long as the provided context is live.
package sources

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wambozi/sigil/internal/event"
)

// FileSource watches a list of directory trees for file-system events using
// fsnotify (backed by inotify on Linux).  It emits event.KindFile events.
type FileSource struct {
	// Paths is the initial set of directories to watch.  Additional paths can
	// be added at runtime via watcher.Add after Start is called.
	Paths []string
}

func (s *FileSource) Name() string { return "files" }

// Events starts the watcher and streams file events until ctx is cancelled.
func (s *FileSource) Events(ctx context.Context) (<-chan event.Event, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("files: create watcher: %w", err)
	}

	for _, p := range s.Paths {
		if err := w.Add(p); err != nil {
			w.Close()
			return nil, fmt.Errorf("files: watch %s: %w", p, err)
		}
	}

	ch := make(chan event.Event, 64)

	go func() {
		defer w.Close()
		defer close(ch)

		for {
			select {
			case <-ctx.Done():
				return

			case fe, ok := <-w.Events:
				if !ok {
					return
				}
				e := event.Event{
					Kind:   event.KindFile,
					Source: s.Name(),
					Payload: map[string]any{
						"path": fe.Name,
						"op":   fe.Op.String(),
					},
					Timestamp: time.Now(),
				}
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}

			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				// Log watcher errors as events so the store has a record,
				// but don't crash the source.
				e := event.Event{
					Kind:   event.KindFile,
					Source: s.Name(),
					Payload: map[string]any{
						"error": err.Error(),
					},
					Timestamp: time.Now(),
				}
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}
