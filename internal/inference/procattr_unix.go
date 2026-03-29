//go:build !windows

package inference

import (
	"os"
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalTerm(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

func signalKill(proc *os.Process) error {
	return proc.Signal(syscall.SIGKILL)
}
