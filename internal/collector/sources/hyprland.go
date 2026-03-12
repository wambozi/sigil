package sources

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// HyprlandSource connects to the Hyprland compositor's IPC socket and emits
// window-focus events whenever the active window changes.  If Hyprland is not
// running (no socket found), Events returns a channel that stays open but
// never emits — graceful degradation on non-Hyprland systems.
type HyprlandSource struct {
	// SocketPath overrides the auto-detected Hyprland socket path.
	// If empty, the path is derived from $XDG_RUNTIME_DIR and
	// $HYPRLAND_INSTANCE_SIGNATURE.
	SocketPath string
}

func (s *HyprlandSource) Name() string { return "hyprland" }

// Events connects to Hyprland's event socket (.socket2.sock) and listens for
// "activewindow>>" lines.  Each line triggers an event.KindHyprland event with
// the window class and title in the payload.
func (s *HyprlandSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 32)

	sockPath := s.socketPath()
	if sockPath == "" {
		// Hyprland not running — return an open but silent channel.
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		// Socket exists but connection failed — degrade gracefully.
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}

	go func() {
		defer conn.Close()
		defer close(ch)

		// Close the connection when context is cancelled so the scanner unblocks.
		go func() {
			<-ctx.Done()
			conn.Close()
		}()

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			e, ok := parseHyprlandEvent(line)
			if !ok {
				continue
			}
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// socketPath returns the Hyprland IPC event socket path, or "" if Hyprland
// is not detected.
func (s *HyprlandSource) socketPath() string {
	if s.SocketPath != "" {
		return s.SocketPath
	}
	return hyprlandSocketPath()
}

// hyprlandSocketPath resolves the Hyprland event socket from environment
// variables.  Returns "" if the required env vars are missing or the socket
// file does not exist.
func hyprlandSocketPath() string {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return ""
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return ""
	}
	path := filepath.Join(runtime, "hypr", sig, ".socket2.sock")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// parseHyprlandEvent parses a single line from the Hyprland event socket.
// It handles the "activewindow>>" event format:
//
//	activewindow>>class,title
//
// Returns false for non-activewindow events.
func parseHyprlandEvent(line string) (event.Event, bool) {
	const prefix = "activewindow>>"
	if !strings.HasPrefix(line, prefix) {
		return event.Event{}, false
	}

	data := line[len(prefix):]
	// Format: "class,title" — class never contains commas but title might.
	windowClass, windowTitle, _ := strings.Cut(data, ",")

	return event.Event{
		Kind:   event.KindHyprland,
		Source: "hyprland",
		Payload: map[string]any{
			"window_class": windowClass,
			"window_title": windowTitle,
			"action":       "focus",
		},
		Timestamp: time.Now(),
	}, true
}

// FormatContextSwitchSummary produces a human-readable summary of window
// focus events for the analyzer.  It takes a slice of events and returns
// the count of distinct window classes and the total number of focus switches.
func FormatContextSwitchSummary(events []event.Event) (switches int, distinctApps int) {
	seen := make(map[string]struct{})
	for _, e := range events {
		cls, _ := e.Payload["window_class"].(string)
		if cls == "" {
			continue
		}
		switches++
		seen[cls] = struct{}{}
	}
	return switches, len(seen)
}

// GroupFocusByWindow returns a map of window_class → focus count from a
// slice of Hyprland events.
func GroupFocusByWindow(events []event.Event) map[string]int {
	counts := make(map[string]int)
	for _, e := range events {
		cls, _ := e.Payload["window_class"].(string)
		if cls != "" {
			counts[cls]++
		}
	}
	return counts
}

// ContextSwitchRate returns the average number of window focus changes per
// hour over the given event slice.  Returns 0 if fewer than 2 events.
func ContextSwitchRate(events []event.Event) float64 {
	if len(events) < 2 {
		return 0
	}
	first := events[0].Timestamp
	last := events[len(events)-1].Timestamp
	hours := last.Sub(first).Hours()
	if hours < 0.01 {
		return 0
	}
	return float64(len(events)) / hours
}

// TopWindows returns the top N window classes by focus count, sorted
// descending.  Used by the analyzer pattern.
func TopWindows(events []event.Event, n int) []WindowFocusEntry {
	counts := GroupFocusByWindow(events)

	entries := make([]WindowFocusEntry, 0, len(counts))
	for cls, count := range counts {
		entries = append(entries, WindowFocusEntry{Class: cls, Count: count})
	}

	// Insertion sort (small N).
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Count > entries[j-1].Count; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	if n > 0 && len(entries) > n {
		entries = entries[:n]
	}
	return entries
}

// WindowFocusEntry is a (class, count) pair for top-window reporting.
type WindowFocusEntry struct {
	Class string
	Count int
}

func (e WindowFocusEntry) String() string {
	return fmt.Sprintf("%s (%d)", e.Class, e.Count)
}
