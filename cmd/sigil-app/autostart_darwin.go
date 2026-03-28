//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const launchAgentLabel = "tech.sigil.app"

// enableAutoStart creates a LaunchAgent plist so sigil-app starts at login.
func enableAutoStart() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>ProcessType</key>
    <string>Interactive</string>
</dict>
</plist>
`, launchAgentLabel, exe)

	plistPath := filepath.Join(dir, launchAgentLabel+".plist")
	return os.WriteFile(plistPath, []byte(plist), 0o644)
}

// disableAutoStart removes the LaunchAgent plist.
func disableAutoStart() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}
