//go:build !linux && !darwin

package notifier

import (
	"log/slog"
	"os/exec"
)

// otherPlatform is a no-op backend used on non-Linux systems (Windows, macOS)
// during development.  On the actual target (NixOS), platform_linux.go is used.
type otherPlatform struct {
	log *slog.Logger
}

func newPlatform(log *slog.Logger) Platform {
	return &otherPlatform{log: log}
}

func (p *otherPlatform) Send(title, body string, _ bool) {
	p.log.Info("notifier: [stub] would send notification", "title", title, "body", body)
}

func (p *otherPlatform) Execute(shellCmd string) error {
	p.log.Info("notifier: [stub] would execute", "cmd", shellCmd)
	return exec.ErrNotFound
}
