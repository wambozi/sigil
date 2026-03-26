//go:build windows

package sources

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// WindowsAppStateSource observes in-app state on Windows by parsing window
// titles. Office apps encode the active document in the title bar, which we
// extract without reading document content. COM automation via go-ole is
// planned for deeper integration (active cell range, cursor position, etc.).
type WindowsAppStateSource struct {
	log       *slog.Logger
	interval  time.Duration
	lastState string
}

// NewWindowsAppStateSource creates a WindowsAppStateSource.
func NewWindowsAppStateSource(log *slog.Logger) *WindowsAppStateSource {
	return &WindowsAppStateSource{
		log:      log,
		interval: 3 * time.Second,
	}
}

func (s *WindowsAppStateSource) Name() string { return "appstate" }

func (s *WindowsAppStateSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.poll(ch)
			}
		}
	}()
	return ch, nil
}

func (s *WindowsAppStateSource) poll(ch chan<- event.Event) {
	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return
	}

	title := getWindowTitle(hwnd)
	procName := getProcessName(hwnd)

	state := parseWindowState(procName, title)
	if state == nil {
		return
	}

	stateJSON, _ := json.Marshal(state)
	if string(stateJSON) == s.lastState {
		return
	}
	s.lastState = string(stateJSON)

	ev := event.Event{
		Kind:      event.KindAppState,
		Source:    "appstate",
		Payload:   state,
		Timestamp: time.Now(),
	}
	select {
	case ch <- ev:
	default:
	}
}
