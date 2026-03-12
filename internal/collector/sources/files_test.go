package sources

import "testing"

func TestFileSource_Name(t *testing.T) {
	s := &FileSource{}
	if got := s.Name(); got != "files" {
		t.Errorf("Name() = %q, want %q", got, "files")
	}
}

func TestShouldIgnore(t *testing.T) {
	s := &FileSource{IgnorePatterns: defaultIgnorePatterns}

	tests := []struct {
		path string
		want bool
	}{
		{"/home/nick/workspace/sigil/internal/store", false},
		{"/home/nick/workspace/sigil/.git", true},
		{"/home/nick/workspace/sigil/.git/objects", true},
		{"/home/nick/workspace/sigil/node_modules/express", true},
		{"/home/nick/workspace/sigil/vendor/golang.org", true},
		{"/home/nick/workspace/sigil/__pycache__", true},
		{"/home/nick/workspace/sigil/target/debug", true},
		{"/home/nick/workspace/sigil/build/output", true},
		{"/home/nick/workspace/sigil/result", true},
		{"/home/nick/workspace/sigil/.cache/go-build", true},
		{"/home/nick/workspace/sigil/cmd/sigild", false},
		{"/home/nick/workspace/sigil/internal/event", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := s.shouldIgnore(tt.path)
			if got != tt.want {
				t.Errorf("shouldIgnore(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestShouldIgnore_customPatterns(t *testing.T) {
	s := &FileSource{IgnorePatterns: []string{"mydir"}}

	if !s.shouldIgnore("/home/nick/mydir") {
		t.Error("expected custom pattern to match suffix")
	}
	if !s.shouldIgnore("/home/nick/mydir/sub") {
		t.Error("expected custom pattern to match middle component")
	}
	if s.shouldIgnore("/home/nick/workspace") {
		t.Error("non-matching path should not be ignored")
	}
}

func TestShouldIgnore_emptyPatterns(t *testing.T) {
	s := &FileSource{IgnorePatterns: nil}
	if s.shouldIgnore("/any/path") {
		t.Error("nil patterns should not ignore anything")
	}
}
