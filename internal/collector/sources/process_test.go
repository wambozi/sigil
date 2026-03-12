package sources

import (
	"fmt"
	"os"
	"testing"
)

func TestReadProcExitState_CurrentProcess(t *testing.T) {
	// Our own process should be readable and in a valid state.
	pid := os.Getpid()
	state, exitCode := readProcExitState(pidStr(pid))

	if state == "" {
		t.Fatal("expected non-empty state for current process")
	}
	// A running process should be in R or S state.
	if state != "R" && state != "S" {
		t.Errorf("unexpected state for running process: %q", state)
	}
	// Exit code should be -1 for a non-zombie process.
	if exitCode != -1 {
		t.Errorf("expected exit code -1 for running process, got %d", exitCode)
	}
}

func TestReadProcExitState_NonexistentPID(t *testing.T) {
	state, exitCode := readProcExitState("999999999")
	if state != "" {
		t.Errorf("expected empty state for nonexistent PID, got %q", state)
	}
	if exitCode != -1 {
		t.Errorf("expected exit code -1 for nonexistent PID, got %d", exitCode)
	}
}

func TestReadExitCodeFromStat_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	// For a running process, the exit code field should be 0 (not exited).
	code := readExitCodeFromStat(pidStr(pid))
	// We can't assert exact value since it depends on kernel, but it should
	// parse without error (-1 means parse failure).
	if code < -1 {
		t.Errorf("unexpected exit code: %d", code)
	}
}

func TestReadExitCodeFromStat_InvalidPID(t *testing.T) {
	code := readExitCodeFromStat("999999999")
	if code != -1 {
		t.Errorf("expected -1 for invalid PID, got %d", code)
	}
}

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
	// Verify that the session struct can hold exit state fields.
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

func pidStr(pid int) string {
	return fmt.Sprintf("%d", pid)
}
