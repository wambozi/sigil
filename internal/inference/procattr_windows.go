//go:build windows

package inference

import (
	"os"
	"os/exec"
)

func setProcGroup(_ *exec.Cmd) {
	// Process groups are managed differently on Windows; no-op for now.
}

func signalTerm(proc *os.Process) error {
	// Windows doesn't have SIGTERM; kill the process directly.
	return proc.Kill()
}

func signalKill(proc *os.Process) error {
	return proc.Kill()
}
