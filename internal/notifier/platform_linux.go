//go:build linux

package notifier

import (
	"context"
	"log/slog"
	"os/exec"
	"time"
)

// linuxPlatform delivers notifications via notify-send (libnotify / D-Bus).
// It is the only backend for v0 on Sigil OS (NixOS + Hyprland/Wayland).
type linuxPlatform struct {
	log *slog.Logger
}

func newPlatform(log *slog.Logger) platform {
	return &linuxPlatform{log: log}
}

func (p *linuxPlatform) send(title, body string, _ bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// -a sets the application name shown in the notification.
	// -i sets the icon from the current theme.
	// -t 8000 makes the notification auto-dismiss after 8 seconds (ambient UX).
	cmd := exec.CommandContext(ctx,
		"notify-send",
		"-a", "sigild",
		"-i", "utilities-system-monitor",
		"-t", "8000",
		title,
		body,
	)
	if err := cmd.Run(); err != nil {
		p.log.Warn("notifier: notify-send failed", "err", err)
	}
}

func (p *linuxPlatform) execute(shellCmd string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd)
	return cmd.Run()
}
