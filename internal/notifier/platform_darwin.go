//go:build darwin

package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// darwinPlatform delivers notifications via osascript on macOS.
type darwinPlatform struct {
	log *slog.Logger
}

func newPlatform(log *slog.Logger) Platform {
	return &darwinPlatform{log: log}
}

func (p *darwinPlatform) Send(title, body string, _ bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Escape double-quotes in title and body to avoid breaking the AppleScript.
	safeTitle := strings.ReplaceAll(title, `"`, `\"`)
	safeBody := strings.ReplaceAll(body, `"`, `\"`)

	script := fmt.Sprintf(
		`display notification %q with title %q`,
		safeBody, safeTitle,
	)

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		p.log.Warn("notifier: osascript failed", "err", err)
	}
}

func (p *darwinPlatform) Execute(shellCmd string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd)
	return cmd.Run()
}
