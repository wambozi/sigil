//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const desktopFileName = "sigil-app.desktop"

// enableAutoStart creates an XDG autostart .desktop file so sigil-app starts
// at login on Linux desktop environments.
func enableAutoStart() error {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	dir := filepath.Join(configDir, "autostart")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Sigil
Comment=Developer workflow intelligence
Exec=%s
Icon=sigil
Terminal=false
Categories=Development;Utility;
StartupNotify=false
X-GNOME-Autostart-enabled=true
`, exe)

	desktopPath := filepath.Join(dir, desktopFileName)
	return os.WriteFile(desktopPath, []byte(desktop), 0o644)
}

// disableAutoStart removes the autostart .desktop file.
func disableAutoStart() error {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	desktopPath := filepath.Join(configDir, "autostart", desktopFileName)
	if err := os.Remove(desktopPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove desktop file: %w", err)
	}
	return nil
}
