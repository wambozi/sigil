//go:build darwin

package sources

import (
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// appQuerier returns state for a specific app, or nil if not available.
type appQuerier func(ctx context.Context) (map[string]any, error)

// AppStateSource polls the frontmost macOS application for internal state via
// AppleScript. Only structural metadata is captured (file names, sheet names,
// etc.) — never document content.
type AppStateSource struct {
	log       *slog.Logger
	interval  time.Duration
	registry  map[string]appQuerier
	lastState string // JSON-serialized last state for diff
}

// NewAppStateSource creates an AppStateSource with the default 3-second
// polling interval and the built-in app registry.
func NewAppStateSource(log *slog.Logger) *AppStateSource {
	s := &AppStateSource{
		log:      log,
		interval: 3 * time.Second,
	}
	s.registry = map[string]appQuerier{
		"Microsoft Excel":   s.queryExcel,
		"Mail":              s.queryMail,
		"Microsoft Outlook": s.queryOutlook,
	}
	return s
}

func (s *AppStateSource) Name() string { return "appstate" }

// Events starts polling the frontmost app and returns a channel of state-change
// events. The channel is closed when ctx is cancelled.
func (s *AppStateSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.poll(ctx, ch)
			}
		}
	}()

	return ch, nil
}

func (s *AppStateSource) poll(ctx context.Context, ch chan<- event.Event) {
	// Get frontmost app.
	appCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(appCtx, "osascript", "-e",
		`tell application "System Events" to get name of first application process whose frontmost is true`).Output()
	if err != nil {
		return
	}
	frontApp := strings.TrimSpace(string(out))

	querier, ok := s.registry[frontApp]
	if !ok {
		return // app not in registry — skip
	}

	queryCtx, queryCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer queryCancel()

	state, err := querier(queryCtx)
	if err != nil {
		s.log.Debug("appstate query failed", "app", frontApp, "err", err)
		return
	}

	state["app"] = frontApp

	// State diff — only emit on change.
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
		s.log.Debug("appstate event dropped (channel full)")
	}
}

func (s *AppStateSource) queryExcel(ctx context.Context) (map[string]any, error) {
	script := `tell application "Microsoft Excel"
	set wb to name of active workbook
	set ws to name of active sheet
	set addr to get address of selection
	return wb & "|" & ws & "|" & addr
end tell`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
	if len(parts) < 3 {
		return nil, nil
	}
	return map[string]any{
		"workbook":  parts[0],
		"sheet":     parts[1],
		"selection": parts[2],
	}, nil
}

func (s *AppStateSource) queryMail(ctx context.Context) (map[string]any, error) {
	script := `tell application "Mail"
	set composing to (count of outgoing messages) > 0
	set subj to ""
	try
		set subj to subject of item 1 of (selection as list)
	end try
	set mb to name of current mailbox of message viewer 1
	return mb & "|" & subj & "|" & (composing as string)
end tell`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
	if len(parts) < 3 {
		return nil, nil
	}
	return map[string]any{
		"mailbox":         parts[0],
		"message_subject": parts[1],
		"composing":       parts[2] == "true",
	}, nil
}

func (s *AppStateSource) queryOutlook(ctx context.Context) (map[string]any, error) {
	script := `tell application "Microsoft Outlook"
	set subj to ""
	try
		set subj to subject of selected objects
	end try
	set composing to (count of outgoing messages) > 0
	return subj & "|" & (composing as string)
end tell`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) < 2 {
		return nil, nil
	}
	return map[string]any{
		"message_subject": parts[0],
		"composing":       parts[1] == "true",
	}, nil
}
