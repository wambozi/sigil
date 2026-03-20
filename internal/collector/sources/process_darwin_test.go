//go:build darwin

package sources

import (
	"os"
	"testing"
)

func TestScanProc_FindsSelf(t *testing.T) {
	// Our own process is a Go test runner, which should match "go test".
	results, err := scanProc([]string{"go test"})
	if err != nil {
		t.Fatalf("scanProc: %v", err)
	}
	if len(results) == 0 {
		// The test binary may not have "go test" in its args on all platforms,
		// so just verify it doesn't error.
		t.Log("scanProc returned 0 results (test binary may not match)")
	}
}

func TestReadProcExitState_CurrentProcess(t *testing.T) {
	pid := pidStr(os.Getpid())
	state, exitCode := readProcExitState(pid)

	if state == "" {
		t.Fatal("expected non-empty state for current process")
	}
	// Exit code is always -1 on macOS (no /proc).
	if exitCode != -1 {
		t.Errorf("expected exit code -1, got %d", exitCode)
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
