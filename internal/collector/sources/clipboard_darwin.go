//go:build darwin

package sources

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// ClipboardSource monitors the macOS system clipboard for changes.
// It polls the clipboard change count via osascript and emits an event when
// it changes.  Only metadata (content type, byte length) is captured — never
// actual clipboard content.  The clipboard is the connective tissue between
// apps: "245 bytes of text/plain from Terminal" links two workflow contexts.
type ClipboardSource struct {
	// Interval is how often the clipboard is polled.  Default: 500ms.
	Interval time.Duration

	lastInfo string // last seen clipboard info string for change detection
}

func (s *ClipboardSource) Name() string { return "clipboard" }

// Events polls the macOS clipboard and emits an event each time the content
// changes.  The channel stays open until ctx is cancelled.
func (s *ClipboardSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if s.Interval == 0 {
		s.Interval = 500 * time.Millisecond
	}

	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(s.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ev, ok := s.poll(ctx)
				if !ok {
					continue
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// poll checks the clipboard and returns an event if the content changed.
func (s *ClipboardSource) poll(ctx context.Context) (event.Event, bool) {
	// Get clipboard info (types and sizes) via osascript.
	infoCtx, infoCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer infoCancel()

	out, err := exec.CommandContext(infoCtx, "osascript", "-e", "the clipboard info").Output()
	if err != nil {
		return event.Event{}, false // clipboard unavailable or timeout
	}

	info := strings.TrimSpace(string(out))
	if info == s.lastInfo {
		return event.Event{}, false // no change
	}
	s.lastInfo = info

	// Get byte length of text content (if any) via pbpaste.
	var length int
	lenCtx, lenCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer lenCancel()

	lenOut, err := exec.CommandContext(lenCtx, "sh", "-c", "pbpaste 2>/dev/null | wc -c").Output()
	if err == nil {
		length, _ = strconv.Atoi(strings.TrimSpace(string(lenOut)))
	}

	// Detect source app via frontmost application.
	sourceApp := frontApp() // reuse from focus_darwin.go

	// Determine content type from clipboard info string.
	contentType := classifyClipboardContent(info)

	return event.Event{
		Kind:   event.KindClipboard,
		Source: "clipboard",
		Payload: map[string]any{
			"source_app":     sourceApp,
			"content_type":   contentType,
			"content_length": length,
		},
		Timestamp: time.Now(),
	}, true
}

// classifyClipboardContent determines the MIME type from osascript's
// "the clipboard info" output.
func classifyClipboardContent(info string) string {
	switch {
	case strings.Contains(info, "\u00ABclass PNGf\u00BB"):
		return "image/png"
	case strings.Contains(info, "\u00ABclass TIFF\u00BB"):
		return "image/tiff"
	case strings.Contains(info, "\u00ABclass utf8\u00BB") || strings.Contains(info, "string"):
		return "text/plain"
	default:
		return "unknown"
	}
}
