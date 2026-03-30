//go:build linux

package sources

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// ClipboardSource monitors the Linux system clipboard for changes.
// It detects available clipboard tools (wl-paste for Wayland, xclip/xsel for
// X11) and polls for changes.  Only metadata (content type, byte length) is
// captured — never actual clipboard content.
type ClipboardSource struct {
	// Interval is how often the clipboard is polled.  Default: 500ms.
	Interval time.Duration

	tool     string // resolved clipboard tool: "wl-paste", "xclip", "xsel"
	lastHash string // hash of last seen clipboard content for change detection
}

func (s *ClipboardSource) Name() string { return "clipboard" }

// Events polls the Linux clipboard and emits an event each time the content
// changes.  If no clipboard tool is available, it returns a silent channel
// that stays open until ctx is cancelled (graceful degradation).
func (s *ClipboardSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if s.Interval == 0 {
		s.Interval = 500 * time.Millisecond
	}

	s.tool = detectClipboardTool()

	ch := make(chan event.Event, 32)

	if s.tool == "" {
		// No clipboard tool available — degrade gracefully.
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}

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
	clipCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	var content []byte
	var mimeType string
	var err error

	switch s.tool {
	case "wl-paste":
		// wl-paste can report MIME types with --list-types.
		mimeType = s.detectMIME(ctx)
		content, err = exec.CommandContext(clipCtx, "wl-paste", "--no-newline").Output()
	case "xclip":
		mimeType = "text/plain" // xclip defaults to text
		content, err = exec.CommandContext(clipCtx, "xclip", "-selection", "clipboard", "-o").Output()
	case "xsel":
		mimeType = "text/plain"
		content, err = exec.CommandContext(clipCtx, "xsel", "--clipboard", "--output").Output()
	}

	if err != nil {
		return event.Event{}, false
	}

	// Hash the content for change detection — we never store the content itself.
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	if hash == s.lastHash {
		return event.Event{}, false
	}
	s.lastHash = hash

	if mimeType == "" {
		mimeType = "unknown"
	}

	return event.Event{
		Kind:   event.KindClipboard,
		Source: "clipboard",
		Payload: map[string]any{
			"source_app":     "", // no reliable way to detect source app on Linux
			"content_type":   mimeType,
			"content_length": len(content),
		},
		Timestamp: time.Now(),
	}, true
}

// detectMIME uses wl-paste --list-types to determine the primary clipboard
// MIME type.  Falls back to "unknown".
func (s *ClipboardSource) detectMIME(ctx context.Context) string {
	mimeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(mimeCtx, "wl-paste", "--list-types").Output()
	if err != nil {
		return "unknown"
	}

	types := strings.TrimSpace(string(out))
	if types == "" {
		return "unknown"
	}

	// Return the first listed type — usually the most specific.
	lines := strings.Split(types, "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return "unknown"
}

// detectClipboardTool finds the first available clipboard tool.
// Prefers wl-paste (Wayland) over xclip/xsel (X11).
func detectClipboardTool() string {
	for _, tool := range []string{"wl-paste", "xclip", "xsel"} {
		if _, err := exec.LookPath(tool); err == nil {
			return tool
		}
	}
	return ""
}
