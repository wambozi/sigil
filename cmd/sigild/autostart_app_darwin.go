//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func installAppAutoStart(home string) error {
	binPath := filepath.Join(home, ".local", "bin", "sigil-app")
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	plistPath := filepath.Join(plistDir, "tech.sigil.app.plist")

	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>tech.sigil.app</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
</dict>
</plist>
`, binPath)

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Printf("  [ok] tray app auto-start enabled (%s)\n", plistPath)
	return nil
}
