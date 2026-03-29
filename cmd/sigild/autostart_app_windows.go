//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

func installAppAutoStart(home string) error {
	binPath := filepath.Join(home, ".local", "bin", "sigil-app.exe")
	cmd := exec.Command("schtasks", "/Create",
		"/TN", "Sigil Tray App",
		"/TR", fmt.Sprintf(`"%s"`, binPath),
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create scheduled task: %w", err)
	}
	fmt.Println("  [ok] tray app auto-start enabled (scheduled task)")
	return nil
}
