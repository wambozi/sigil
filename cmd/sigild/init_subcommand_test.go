package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallShellHook(t *testing.T) {
	tests := []struct {
		name           string
		shell          string
		wantRCFile     string // relative to home
		wantHookFile   string // embedded asset name
		wantSourceLine string
		wantSkip       bool   // true if shell is unknown
	}{
		{
			name:           "zsh",
			shell:          "/bin/zsh",
			wantRCFile:     ".zshrc",
			wantHookFile:   "shell-hook.zsh",
			wantSourceLine: `source "$HOME/.config/sigil/shell-hook.zsh"`,
		},
		{
			name:           "bash",
			shell:          "/bin/bash",
			wantRCFile:     ".bashrc",
			wantHookFile:   "shell-hook.bash",
			wantSourceLine: `source "$HOME/.config/sigil/shell-hook.bash"`,
		},
		{
			name:           "fish",
			shell:          "/usr/bin/fish",
			wantRCFile:     filepath.Join(".config", "fish", "config.fish"),
			wantHookFile:   "shell-hook.fish",
			wantSourceLine: `source $HOME/.config/sigil/shell-hook.fish`,
		},
		{
			name:     "unknown shell",
			shell:    "/bin/csh",
			wantSkip: true,
		},
		{
			name:     "empty SHELL",
			shell:    "",
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SHELL", tt.shell)

			err := installShellHook(home)
			if err != nil {
				t.Fatalf("installShellHook returned error: %v", err)
			}

			if tt.wantSkip {
				// For unknown shells, no RC file should be written.
				entries, _ := os.ReadDir(home)
				for _, e := range entries {
					if e.Name() == ".config" {
						// .config might exist but shouldn't have sigil subdir with hook
						hookPath := filepath.Join(home, ".config", "sigil")
						if _, err := os.Stat(hookPath); err == nil {
							t.Error("hook directory should not exist for unknown shell")
						}
					}
				}
				return
			}

			// Verify the hook file was copied to ~/.config/sigil/<hookFile>.
			hookDst := filepath.Join(home, ".config", "sigil", tt.wantHookFile)
			if _, err := os.Stat(hookDst); err != nil {
				t.Errorf("hook file not found at %s: %v", hookDst, err)
			}

			// Verify the source line was appended to the RC file.
			rcPath := filepath.Join(home, tt.wantRCFile)
			rc, err := os.ReadFile(rcPath)
			if err != nil {
				t.Fatalf("cannot read RC file %s: %v", rcPath, err)
			}
			rcContent := string(rc)
			if !strings.Contains(rcContent, tt.wantSourceLine) {
				t.Errorf("RC file does not contain expected source line\nwant: %s\ngot:\n%s", tt.wantSourceLine, rcContent)
			}
		})
	}
}

func TestInstallShellHook_Idempotency(t *testing.T) {
	tests := []struct {
		name  string
		shell string
	}{
		{"zsh", "/bin/zsh"},
		{"bash", "/bin/bash"},
		{"fish", "/usr/bin/fish"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SHELL", tt.shell)

			// Run twice.
			if err := installShellHook(home); err != nil {
				t.Fatalf("first install failed: %v", err)
			}
			if err := installShellHook(home); err != nil {
				t.Fatalf("second install failed: %v", err)
			}

			// Determine which RC file to check.
			var rcRel string
			var sourceLine string
			switch {
			case strings.Contains(tt.shell, "zsh"):
				rcRel = ".zshrc"
				sourceLine = `source "$HOME/.config/sigil/shell-hook.zsh"`
			case strings.Contains(tt.shell, "bash"):
				rcRel = ".bashrc"
				sourceLine = `source "$HOME/.config/sigil/shell-hook.bash"`
			case strings.Contains(tt.shell, "fish"):
				rcRel = filepath.Join(".config", "fish", "config.fish")
				sourceLine = `source $HOME/.config/sigil/shell-hook.fish`
			}

			rc, err := os.ReadFile(filepath.Join(home, rcRel))
			if err != nil {
				t.Fatalf("cannot read RC file: %v", err)
			}

			count := strings.Count(string(rc), sourceLine)
			if count != 1 {
				t.Errorf("source line appears %d times, want exactly 1", count)
			}
		})
	}
}
