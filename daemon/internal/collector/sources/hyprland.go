package sources

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/wambozi/aether/internal/event"
)

// HyprlandSource connects to Hyprland's event socket and streams compositor
// events (window focus changes, workspace switches, layout changes) as
// event.KindHyprland observations.
//
// Hyprland exposes two sockets per instance:
//
//	.socket.sock  — command socket (request/reply)
//	.socket2.sock — event socket  (line-delimited stream, read-only)
//
// We connect to .socket2.sock.
type HyprlandSource struct {
	// SocketDir overrides the directory where Hyprland's socket files live.
	// If empty, it is derived from $HYPRLAND_INSTANCE_SIGNATURE.
	SocketDir string
}

func (s *HyprlandSource) Name() string { return "hyprland" }

// Events connects to the Hyprland event socket and forwards events until ctx
// is cancelled or the connection drops.
func (s *HyprlandSource) Events(ctx context.Context) (<-chan event.Event, error) {
	socketPath, err := s.eventSocket()
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("hyprland: connect %s: %w", socketPath, err)
	}

	ch := make(chan event.Event, 32)

	go func() {
		defer conn.Close()
		defer close(ch)

		// Cancel the blocking Read when ctx is done.
		go func() {
			<-ctx.Done()
			conn.Close()
		}()

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			e := parseHyprlandLine(line, s.Name())
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// eventSocket returns the path to Hyprland's event socket (.socket2.sock).
func (s *HyprlandSource) eventSocket() (string, error) {
	dir := s.SocketDir
	if dir == "" {
		sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
		if sig == "" {
			return "", fmt.Errorf("hyprland: $HYPRLAND_INSTANCE_SIGNATURE is not set; is Hyprland running?")
		}
		dir = fmt.Sprintf("/tmp/hypr/%s", sig)
	}
	return dir + "/.socket2.sock", nil
}

// parseHyprlandLine converts a raw Hyprland event line into an Event.
//
// Hyprland event lines are formatted as:
//
//	eventname>>data
//
// For example:
//
//	activewindow>>kitty,zsh
//	workspace>>2
//	focusedmon>>DP-1,1
func parseHyprlandLine(line, source string) event.Event {
	e := event.Event{
		Kind:      event.KindHyprland,
		Source:    source,
		Payload:   make(map[string]any),
		Timestamp: time.Now(),
	}

	parts := strings.SplitN(line, ">>", 2)
	if len(parts) != 2 {
		e.Payload["raw"] = line
		return e
	}

	name, data := parts[0], parts[1]
	e.Payload["event"] = name

	// Parse known event types into structured fields.
	switch name {
	case "activewindow":
		fields := strings.SplitN(data, ",", 2)
		if len(fields) == 2 {
			e.Payload["class"] = fields[0]
			e.Payload["title"] = fields[1]
		}
	case "workspace":
		e.Payload["workspace"] = data
	case "focusedmon":
		fields := strings.SplitN(data, ",", 2)
		if len(fields) == 2 {
			e.Payload["monitor"] = fields[0]
			e.Payload["workspace"] = fields[1]
		}
	case "openwindow", "closewindow", "movewindow":
		e.Payload["address"] = data
	default:
		e.Payload["data"] = data
	}

	return e
}
