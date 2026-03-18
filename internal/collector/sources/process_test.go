package sources

import (
	"testing"
)

func TestCategorize(t *testing.T) {
	tests := []struct {
		cmdline string
		want    string
	}{
		{"claude code", "ai"},
		{"go test ./...", "test"},
		{"go build ./cmd/sigild/", "build"},
		{"docker compose up", "deploy"},
		{"git push origin main", "vcs"},
		{"python3 script.py", "runtime"},
		{"unknown-tool", "runtime"},
	}
	for _, tt := range tests {
		got := categorize(tt.cmdline)
		if got != tt.want {
			t.Errorf("categorize(%q) = %q, want %q", tt.cmdline, got, tt.want)
		}
	}
}

func TestProcessSession_ExitStateInPayload(t *testing.T) {
	sess := &processSession{
		PID:       "12345",
		Comm:      "go",
		Cmdline:   "go test ./...",
		Category:  "test",
		LastState: "Z",
		ExitCode:  1,
	}
	if sess.LastState != "Z" {
		t.Errorf("LastState: got %q, want %q", sess.LastState, "Z")
	}
	if sess.ExitCode != 1 {
		t.Errorf("ExitCode: got %d, want %d", sess.ExitCode, 1)
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"123", true},
		{"0", true},
		{"", false},
		{"12a3", false},
		{"abc", false},
		{"-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := isNumeric(tt.s); got != tt.want {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestMatchesAny(t *testing.T) {
	keywords := []string{"go test", "make", "cargo"}

	tests := []struct {
		s    string
		want bool
	}{
		{"go test ./...", true},
		{"GO TEST ./...", true},
		{"make build", true},
		{"cargo build --release", true},
		{"ls -la", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := matchesAny(tt.s, keywords); got != tt.want {
				t.Errorf("matchesAny(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestMatchesAny_emptyKeywords(t *testing.T) {
	if matchesAny("anything", nil) {
		t.Error("expected false for nil keywords")
	}
}

func TestProcessSource_Name(t *testing.T) {
	s := &ProcessSource{}
	if got := s.Name(); got != "process" {
		t.Errorf("Name() = %q, want %q", got, "process")
	}
}

func TestAllKeywords(t *testing.T) {
	kw := allKeywords()
	if len(kw) == 0 {
		t.Fatal("allKeywords() returned empty list")
	}
	found := make(map[string]bool)
	for _, k := range kw {
		found[k] = true
	}
	for _, want := range []string{"go test", "go build", "docker", "git", "claude"} {
		if !found[want] {
			t.Errorf("allKeywords() missing %q", want)
		}
	}
}
