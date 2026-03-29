//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func installAppAutoStart(home string) error {
	autostartDir := filepath.Join(home, ".config", "autostart")
	desktopPath := filepath.Join(autostartDir, "sigil-app.desktop")

	if err := os.MkdirAll(autostartDir, 0o755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}

	binPath := filepath.Join(home, ".local", "bin", "sigil-app")
	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Sigil
Exec=%s
Icon=sigil
Comment=Sigil tray app
X-GNOME-Autostart-enabled=true
StartupNotify=false
Terminal=false
`, binPath)

	if err := os.WriteFile(desktopPath, []byte(desktop), 0o644); err != nil {
		return fmt.Errorf("write desktop file: %w", err)
	}
	fmt.Printf("  [ok] tray app auto-start enabled (%s)\n", desktopPath)
	return nil
}
