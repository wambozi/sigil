//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// darwinNotifier uses osascript to display notifications. When run from within
// a Wails .app bundle, osascript notifications inherit the app icon, solving
// the Script Editor icon problem.
type darwinNotifier struct{}

func newNotifier() Notifier {
	return &darwinNotifier{}
}

func (n *darwinNotifier) Show(title, body, iconPath string, suggestionID int64) error {
	// Escape double quotes in strings for AppleScript.
	safeTitle := strings.ReplaceAll(title, `"`, `\"`)
	safeBody := strings.ReplaceAll(body, `"`, `\"`)

	script := fmt.Sprintf(
		`display notification "%s" with title "%s" subtitle "Sigil"`,
		safeBody, safeTitle,
	)

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

func (n *darwinNotifier) Close() {}
