// Package sources contains individual collector source implementations.
// Each source implements the collector.Source interface and emits events
// over a channel for as long as the provided context is live.
package sources

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wambozi/sigil/internal/event"
)

// FileSource watches a list of directory trees for file-system events using
// fsnotify (backed by inotify on Linux / kqueue on macOS).  It recursively
// walks each path and watches all subdirectories, automatically adding new
// directories as they are created.
type FileSource struct {
	// Paths is the initial set of root directories to watch recursively.
	Paths []string

	// IgnorePatterns are path substrings that should be skipped during the
	// recursive walk (e.g. ".git", "node_modules", "vendor").
	IgnorePatterns []string

	// MaxWatches caps the total number of directories watched.
	// macOS kqueue uses one file descriptor per watched dir, so large trees
	// can easily exhaust the process limit.  0 means DefaultMaxWatches.
	MaxWatches int
}

// DefaultMaxWatches is the default ceiling for watched directories.
// Keeps the daemon well under the typical macOS fd limit (10240).
const DefaultMaxWatches = 4096

// defaultIgnorePatterns covers directories that generate massive inotify/kqueue
// noise with no useful workflow signal.
var defaultIgnorePatterns = []string{
	".git",
	"node_modules",
	"vendor",
	"__pycache__",
	".next",
	"dist",
	".cache",
	".venv",
	"venv",
	"target", // Rust/Maven
	"build",  // Gradle/generic
	".nix-profile",
	"result", // Nix build output symlink
	// Package manager caches
	".bun",
	".npm",
	".yarn",
	".cargo",
	".rustup",
	// System directories (macOS)
	".Trash",
	"Library",
	".local",
	// IDE metadata
	".idea",
	".vscode",
}

// defaultIgnoreBasenames are file names (not directories) that should be
// ignored regardless of their location.  Checked via filepath.Base match.
var defaultIgnoreBasenames = map[string]bool{
	".DS_Store":     true,
	".localized":    true,
	".zsh_history":  true,
	".bash_history": true,
	".claude.json":  true,
}

func (s *FileSource) Name() string { return "files" }

// Events starts the watcher and streams file events until ctx is cancelled.
func (s *FileSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if len(s.IgnorePatterns) == 0 {
		s.IgnorePatterns = defaultIgnorePatterns
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("files: create watcher: %w", err)
	}

	// Recursively walk each root path and watch all subdirectories.
	total := 0
	for _, root := range s.Paths {
		n, walkErr := s.walkAndWatch(w, root, total)
		if walkErr != nil {
			w.Close()
			return nil, fmt.Errorf("files: walk %s: %w", root, walkErr)
		}
		total = n
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

				// Auto-watch newly created directories so we pick up
				// files in dirs created after startup.
				if fe.Op&fsnotify.Create != 0 && !s.shouldIgnore(fe.Name) {
					// Best-effort: if it's a directory, add it.
					// Ignore errors (might be a file, or already watched).
					_ = w.Add(fe.Name)
				}

				// Skip events for ignored paths.
				if s.shouldIgnore(fe.Name) {
					continue
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

// walkAndWatch recursively adds all subdirectories under root to the watcher,
// skipping ignored patterns.  Returns the number of directories watched.
// It stops adding watches once MaxWatches is reached.
func (s *FileSource) walkAndWatch(w *fsnotify.Watcher, root string, current int) (int, error) {
	max := s.MaxWatches
	if max <= 0 {
		max = DefaultMaxWatches
	}

	count := current
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		if s.shouldIgnore(path) {
			return filepath.SkipDir
		}
		if count >= max {
			return filepath.SkipAll
		}
		if addErr := w.Add(path); addErr != nil {
			return nil // skip dirs we can't watch (permissions, etc.)
		}
		count++
		return nil
	})
	return count, err
}

// shouldIgnore returns true if any ignore pattern appears as a path component
// or the file's basename matches a known noise file.
func (s *FileSource) shouldIgnore(path string) bool {
	// Check basename against known noise files.
	if defaultIgnoreBasenames[filepath.Base(path)] {
		return true
	}
	// Check directory components against ignore patterns.
	for _, pat := range s.IgnorePatterns {
		if strings.Contains(path, string(filepath.Separator)+pat+string(filepath.Separator)) ||
			strings.HasSuffix(path, string(filepath.Separator)+pat) {
			return true
		}
	}
	return false
}
