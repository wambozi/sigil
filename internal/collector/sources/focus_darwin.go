//go:build darwin

package sources

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// DarwinFocusSource polls the macOS front-app API via lsappinfo and emits
// KindHyprland events whenever the focused application changes.  This is the
// macOS equivalent of HyprlandSource and produces identical event payloads so
// the analyzer's window-context-switching detector works unchanged.
//
// No Accessibility permissions are required — lsappinfo reads the app list
// from the window server without assistive access.  Window titles are not
// available without Accessibility, so window_title is left empty.
type DarwinFocusSource struct {
	// Interval is how often the focused app is polled.  Default: 2 seconds.
	Interval time.Duration
}

func (s *DarwinFocusSource) Name() string { return "focus" }

// Events polls the frontmost application and emits an event each time it
// changes.  The channel stays open until ctx is cancelled.
func (s *DarwinFocusSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if s.Interval == 0 {
		s.Interval = 2 * time.Second
	}

	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		var lastApp string

		ticker := time.NewTicker(s.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app := frontApp()
				if app == "" || app == lastApp {
					continue
				}
				lastApp = app

				e := event.Event{
					Kind:   event.KindHyprland, // reuse existing kind for analyzer compatibility
					Source: s.Name(),
					Payload: map[string]any{
						"window_class": app,
						"window_title": "", // requires Accessibility permissions
						"action":       "focus",
					},
					Timestamp: time.Now(),
				}
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// frontApp returns the display name of the frontmost application using
// lsappinfo, a built-in macOS command that queries the window server.
// Returns "" on any error.
func frontApp() string {
	// lsappinfo front returns the ASN (application serial number) of the
	// frontmost app, e.g. "ASN:0x0-0x1b01b:".
	asnOut, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return ""
	}
	asn := strings.TrimSpace(string(asnOut))
	if asn == "" {
		return ""
	}

	// lsappinfo info -only name <ASN> returns: "LSDisplayName"="Terminal"
	infoOut, err := exec.Command("lsappinfo", "info", "-only", "name", asn).Output()
	if err != nil {
		return ""
	}

	// Parse the output: "LSDisplayName"="AppName"
	line := strings.TrimSpace(string(infoOut))
	if !strings.Contains(line, "=") {
		return ""
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.Trim(parts[1], "\"")
}
