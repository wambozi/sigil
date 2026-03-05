// Package actuator dispatches actions produced by the analyzer.
// For v0, the only output channel is desktop notifications via notify-send
// (which itself talks to D-Bus).  Future milestones will add direct D-Bus
// calls, shell suggestion-bar pushes, and active environment mutations.
package actuator

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/wambozi/aether/internal/analyzer"
)

// Actuator listens for analyzer summaries and dispatches actions.
type Actuator struct {
	log *slog.Logger

	// MinInsightLength controls how short an LLM insight can be before we
	// consider it too thin to surface as a notification.
	MinInsightLength int
}

// New creates an Actuator.
func New(log *slog.Logger) *Actuator {
	return &Actuator{
		log:              log,
		MinInsightLength: 20,
	}
}

// OnSummary is wired into analyzer.Analyzer.OnSummary.  It is called from the
// analyzer goroutine and must be non-blocking.
func (a *Actuator) OnSummary(s analyzer.Summary) {
	if s.Insights == "" || len(s.Insights) < a.MinInsightLength {
		return
	}
	go a.notify(s)
}

// notify sends a D-Bus desktop notification using notify-send.
// This is a pragmatic v0 approach — a future milestone will use go-dbus directly
// so we can set notification categories and action buttons.
func (a *Actuator) notify(s analyzer.Summary) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary := fmt.Sprintf("Aether (%s analysis)", formatDuration(s.Period))
	body := s.Insights

	// notify-send is part of libnotify and is universally available on
	// NixOS with a Wayland compositor.  The -a flag sets the app name.
	cmd := exec.CommandContext(ctx,
		"notify-send",
		"-a", "aetherd",
		"-i", "utilities-system-monitor",
		summary,
		body,
	)

	if err := cmd.Run(); err != nil {
		a.log.Warn("actuator: notify-send failed", "err", err)
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return d.String()
	}
}
