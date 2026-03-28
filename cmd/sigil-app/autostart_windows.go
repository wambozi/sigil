//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

const taskName = "SigilApp"

// enableAutoStart creates a scheduled task so sigil-app starts at logon on
// Windows.
func enableAutoStart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Use schtasks to create a logon trigger task.
	cmd := exec.Command("schtasks", "/Create",
		"/TN", taskName,
		"/TR", exe,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F", // force overwrite if exists
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks create: %s: %w", string(out), err)
	}
	return nil
}

// disableAutoStart removes the scheduled task.
func disableAutoStart() error {
	cmd := exec.Command("schtasks", "/Delete",
		"/TN", taskName,
		"/F",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks delete: %s: %w", string(out), err)
	}
	return nil
}
