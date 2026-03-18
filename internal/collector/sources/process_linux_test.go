//go:build linux

package sources

import (
	"fmt"
	"os"
	"testing"
)

func pidStr(pid int) string {
	return fmt.Sprintf("%d", pid)
}

func TestReadProcExitState_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	state, exitCode := readProcExitState(pidStr(pid))

	if state == "" {
		t.Fatal("expected non-empty state for current process")
	}
	if state != "R" && state != "S" {
		t.Errorf("unexpected state for running process: %q", state)
	}
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
	code := readExitCodeFromStat(pidStr(pid))
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
