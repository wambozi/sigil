package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectShells(t *testing.T) {
	tests := []struct {
		name      string
		shell     string          // value for $SHELL
		binaries  map[string]bool // fake binaries to create (relative to tmpdir)
		wantNames []string        // expected shell names in order
	}{
		{
			name:      "zsh_only_via_env",
			shell:     "/usr/bin/zsh",
			binaries:  map[string]bool{},
			wantNames: []string{"zsh"},
		},
		{
			name:      "bash_only_via_env",
			shell:     "/bin/bash",
			binaries:  map[string]bool{},
			wantNames: []string{"bash"},
		},
		{
			name:      "zsh_env_bash_detected",
			shell:     "/usr/bin/zsh",
			binaries:  map[string]bool{"bash": true},
			wantNames: []string{"zsh", "bash"},
		},
		{
			name:      "bash_env_zsh_detected",
			shell:     "/bin/bash",
			binaries:  map[string]bool{"zsh": true},
			wantNames: []string{"bash", "zsh"},
		},
		{
			name:      "neither_shell",
			shell:     "/bin/fish",
			binaries:  map[string]bool{},
			wantNames: nil,
		},
		{
			name:      "both_detected_no_env_match",
			shell:     "/bin/fish",
			binaries:  map[string]bool{"zsh": true, "bash": true},
			wantNames: []string{"zsh", "bash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()

			// Override $SHELL.
			t.Setenv("SHELL", tt.shell)

			// Build a custom registry pointing DetectPaths into tmp.
			var reg []shellDef
			for _, sd := range shellRegistry {
				clone := sd
				var paths []string
				for _, origPath := range sd.DetectPaths {
					fakePath := filepath.Join(tmp, filepath.Base(origPath))
					paths = append(paths, fakePath)
				}
				clone.DetectPaths = paths
				reg = append(reg, clone)
			}

			// Create fake binaries.
			for bin := range tt.binaries {
				fakePath := filepath.Join(tmp, bin)
				if err := os.WriteFile(fakePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			got := detectShells(reg)
			var gotNames []string
			for _, sd := range got {
				gotNames = append(gotNames, sd.Name)
			}

			if len(gotNames) != len(tt.wantNames) {
				t.Fatalf("detectShells() = %v, want %v", gotNames, tt.wantNames)
			}
			for i, name := range gotNames {
				if name != tt.wantNames[i] {
					t.Errorf("detectShells()[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func TestInstallShellHookFor(t *testing.T) {
	tests := []struct {
		name         string
		existingRC   string // existing RC file content ("" means no file)
		wantInRC     string // substring expected in RC file after install
		wantErr      bool
		idempotent   bool // if true, run twice and ensure no duplication
		missingAsset bool // if true, use a nonexistent hook script name
	}{
		{
			name:       "fresh_install",
			existingRC: "",
			wantInRC:   `source "$HOME/.config/sigil/shell-hook.zsh"`,
		},
		{
			name:       "existing_rc_content",
			existingRC: "# my zshrc\nexport FOO=bar\n",
			wantInRC:   `source "$HOME/.config/sigil/shell-hook.zsh"`,
		},
		{
			name:       "idempotent",
			existingRC: "",
			wantInRC:   `source "$HOME/.config/sigil/shell-hook.zsh"`,
			idempotent: true,
		},
		{
			name:         "missing_asset",
			wantErr:      true,
			missingAsset: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()

			sd := shellDef{
				Name:       "zsh",
				Binary:     "zsh",
				RCFiles:    []string{".zshrc"},
				HookScript: "shell-hook.zsh",
				SourceLine: `source "$HOME/.config/sigil/shell-hook.zsh"`,
			}

			if tt.missingAsset {
				sd.HookScript = "nonexistent-hook.zsh"
			}

			// Pre-create the embedded hook script in the config dir
			// (simulating what copyEmbeddedHook does) — except for missing_asset test.
			if !tt.missingAsset {
				hookDir := filepath.Join(home, ".config", "sigil")
				if err := os.MkdirAll(hookDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			// Write existing RC file if specified.
			if tt.existingRC != "" {
				rcFile := filepath.Join(home, ".zshrc")
				if err := os.WriteFile(rcFile, []byte(tt.existingRC), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := installShellHookFor(home, sd)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify RC file contents.
			rcFile := filepath.Join(home, ".zshrc")
			data, err := os.ReadFile(rcFile)
			if err != nil {
				t.Fatalf("read RC file: %v", err)
			}
			if !strings.Contains(string(data), tt.wantInRC) {
				t.Errorf("RC file missing expected line %q, got:\n%s", tt.wantInRC, data)
			}

			if tt.idempotent {
				// Run again — should not duplicate.
				err = installShellHookFor(home, sd)
				if err != nil {
					t.Fatalf("idempotent re-run: %v", err)
				}
				data, _ = os.ReadFile(rcFile)
				count := strings.Count(string(data), sd.SourceLine)
				if count != 1 {
					t.Errorf("source line appears %d times (want 1):\n%s", count, data)
				}
			}
		})
	}
}
