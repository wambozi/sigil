package sources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wambozi/sigil/internal/event"
)

// GitSource watches one or more git repositories for activity by monitoring
// key files inside their .git/ directories.  It emits event.KindGit events
// for commits, branch switches, and staging changes.
type GitSource struct {
	// RepoPaths is the list of repository root directories to watch.
	RepoPaths []string
}

func (s *GitSource) Name() string { return "git" }

// Events starts watchers on all configured repos and merges their event
// streams into a single channel.
func (s *GitSource) Events(ctx context.Context) (<-chan event.Event, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("git: create watcher: %w", err)
	}

	for _, repo := range s.RepoPaths {
		gitDir := filepath.Join(repo, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue // not a git repo or .git not accessible
		}
		if err := w.Add(gitDir); err != nil {
			// Non-fatal: log via the event channel below.
			_ = err
		}
	}

	ch := make(chan event.Event, 32)

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
				e, ok := classifyGitEvent(fe, s.Name())
				if !ok {
					continue // not interesting
				}
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}

			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return ch, nil
}

// classifyGitEvent maps an fsnotify event on a .git/ file to a semantic
// GitKind.  Returns false if the event should be ignored.
func classifyGitEvent(fe fsnotify.Event, source string) (event.Event, bool) {
	base := filepath.Base(fe.Name)

	var gitKind string
	switch {
	case base == "COMMIT_EDITMSG":
		// Written when a commit is created.
		gitKind = "commit"
	case base == "HEAD":
		// Rewritten on branch switch or reset.
		gitKind = "head_change"
	case base == "index":
		// Modified on git add / git reset.
		gitKind = "index_change"
	case base == "MERGE_HEAD" || base == "MERGE_MSG":
		gitKind = "merge"
	case base == "rebase-merge" || base == "rebase-apply":
		gitKind = "rebase"
	default:
		return event.Event{}, false
	}

	// Best-effort: infer the repo root from the .git directory path.
	repoRoot := ""
	dir := filepath.Dir(fe.Name)
	if strings.HasSuffix(dir, ".git") {
		repoRoot = filepath.Dir(dir)
	}

	return event.Event{
		Kind:   event.KindGit,
		Source: source,
		Payload: map[string]any{
			"git_kind":  gitKind,
			"file":      base,
			"op":        fe.Op.String(),
			"repo_root": repoRoot,
		},
		Timestamp: time.Now(),
	}, true
}
