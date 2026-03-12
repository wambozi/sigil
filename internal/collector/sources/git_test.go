package sources

import (
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/wambozi/sigil/internal/event"
)

func TestGitSource_Name(t *testing.T) {
	s := &GitSource{}
	if got := s.Name(); got != "git" {
		t.Errorf("Name() = %q, want %q", got, "git")
	}
}

func TestClassifyGitEvent(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantKind string
		wantOK   bool
	}{
		{"commit", "/home/nick/repo/.git/COMMIT_EDITMSG", "commit", true},
		{"head change", "/home/nick/repo/.git/HEAD", "head_change", true},
		{"index change", "/home/nick/repo/.git/index", "index_change", true},
		{"merge head", "/home/nick/repo/.git/MERGE_HEAD", "merge", true},
		{"merge msg", "/home/nick/repo/.git/MERGE_MSG", "merge", true},
		{"rebase merge", "/home/nick/repo/.git/rebase-merge", "rebase", true},
		{"rebase apply", "/home/nick/repo/.git/rebase-apply", "rebase", true},
		{"unknown file", "/home/nick/repo/.git/config", "", false},
		{"random file", "/home/nick/repo/.git/refs/heads/main", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := fsnotify.Event{Name: tt.file, Op: fsnotify.Write}
			ev, ok := classifyGitEvent(fe, "git")
			if ok != tt.wantOK {
				t.Fatalf("classifyGitEvent ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if ev.Kind != event.KindGit {
				t.Errorf("Kind = %q, want %q", ev.Kind, event.KindGit)
			}
			if ev.Source != "git" {
				t.Errorf("Source = %q, want %q", ev.Source, "git")
			}
			gotKind, _ := ev.Payload["git_kind"].(string)
			if gotKind != tt.wantKind {
				t.Errorf("git_kind = %q, want %q", gotKind, tt.wantKind)
			}
		})
	}
}

func TestClassifyGitEvent_repoRoot(t *testing.T) {
	fe := fsnotify.Event{Name: "/home/nick/repo/.git/HEAD", Op: fsnotify.Write}
	ev, ok := classifyGitEvent(fe, "git")
	if !ok {
		t.Fatal("expected ok=true for HEAD")
	}
	root, _ := ev.Payload["repo_root"].(string)
	if root != "/home/nick/repo" {
		t.Errorf("repo_root = %q, want %q", root, "/home/nick/repo")
	}
}
