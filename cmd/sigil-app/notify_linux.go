//go:build linux

package main

import (
	"os/exec"
)

// linuxNotifier uses notify-send to display desktop notifications.
// For full D-Bus integration with action buttons, a future version should use
// godbus/dbus/v5 directly.
type linuxNotifier struct{}

func newNotifier() Notifier {
	return &linuxNotifier{}
}

func (n *linuxNotifier) Show(title, body, iconPath string, suggestionID int64) error {
	args := []string{"-a", "Sigil"}
	if iconPath != "" {
		args = append(args, "-i", iconPath)
	}
	args = append(args, title, body)

	cmd := exec.Command("notify-send", args...)
	return cmd.Run()
}

func (n *linuxNotifier) Close() {}
