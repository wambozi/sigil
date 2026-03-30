//go:build linux

package sources

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// focusBackend queries the active window. Returns (class, title, error).
type focusBackend func(ctx context.Context) (string, string, error)

// LinuxFocusSource detects the active window across Linux desktop environments.
type LinuxFocusSource struct {
	log      *slog.Logger
	interval time.Duration
	backend  focusBackend
	lastKey  string // "class|title" for dedup
}

// NewLinuxFocusSource auto-detects the compositor and returns a focus source.
// Returns nil if Hyprland is detected (already handled) or no supported
// compositor found.
func NewLinuxFocusSource(log *slog.Logger) *LinuxFocusSource {
	// Skip if Hyprland — HyprlandSource handles it.
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		log.Debug("linux-focus: Hyprland detected, skipping (handled by HyprlandSource)")
		return nil
	}

	backend := detectBackend(log)
	if backend == nil {
		log.Warn("linux-focus: no supported compositor detected")
		return nil
	}

	return &LinuxFocusSource{
		log:      log,
		interval: 2 * time.Second,
		backend:  backend,
	}
}

func detectBackend(log *slog.Logger) focusBackend {
	sessionType := os.Getenv("XDG_SESSION_TYPE")
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")

	// Wayland compositors.
	if sessionType == "wayland" {
		if os.Getenv("SWAYSOCK") != "" {
			log.Info("linux-focus: using sway/i3 backend")
			return swayFocus
		}
		if strings.Contains(desktop, "GNOME") {
			log.Info("linux-focus: using GNOME Wayland backend")
			return gnomeFocus
		}
		if strings.Contains(desktop, "KDE") {
			log.Info("linux-focus: using KDE Wayland backend (kdotool)")
			return kdeFocus
		}
	}

	// X11 fallback (works with any WM).
	if sessionType == "x11" || sessionType == "" {
		if _, err := exec.LookPath("xdotool"); err == nil {
			log.Info("linux-focus: using X11/xdotool backend")
			return x11Focus
		}
	}

	// sway socket without session type set.
	if os.Getenv("SWAYSOCK") != "" {
		log.Info("linux-focus: using sway backend (SWAYSOCK present)")
		return swayFocus
	}

	return nil
}

func (s *LinuxFocusSource) Name() string { return "linux-focus" }

func (s *LinuxFocusSource) Events(ctx context.Context) (<-chan event.Event, error) {
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
				s.poll(ctx, ch)
			}
		}
	}()
	return ch, nil
}

func (s *LinuxFocusSource) poll(ctx context.Context, ch chan<- event.Event) {
	queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	class, title, err := s.backend(queryCtx)
	if err != nil || (class == "" && title == "") {
		return
	}

	key := class + "|" + title
	if key == s.lastKey {
		return
	}
	s.lastKey = key

	ev := event.Event{
		Kind:   event.KindHyprland, // reuse existing focus event kind
		Source: "linux-focus",
		Payload: map[string]any{
			"window_class": class,
			"window_title": title,
			"action":       "focus",
		},
		Timestamp: time.Now(),
	}
	select {
	case ch <- ev:
	default:
	}
}

// --- X11 backend (xdotool) ---

func x11Focus(ctx context.Context) (string, string, error) {
	// Get window class.
	classOut, err := exec.CommandContext(ctx, "xdotool", "getactivewindow", "getwindowclassname").Output()
	if err != nil {
		return "", "", err
	}
	class := strings.TrimSpace(string(classOut))

	// Get window title.
	titleOut, err := exec.CommandContext(ctx, "xdotool", "getactivewindow", "getname").Output()
	if err != nil {
		return class, "", nil // class without title is OK
	}
	title := strings.TrimSpace(string(titleOut))

	return class, title, nil
}

// --- GNOME Wayland backend (gdbus) ---

func gnomeFocus(ctx context.Context) (string, string, error) {
	// Use gdbus to call GNOME Shell eval.
	script := `
	let w = global.display.focus_window;
	if (w) JSON.stringify({class: w.get_wm_class() || "", title: w.get_title() || ""});
	else "null";
	`
	out, err := exec.CommandContext(ctx, "gdbus", "call", "--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.Eval",
		script).Output()
	if err != nil {
		return "", "", err
	}

	// gdbus output: (true, '{"class":"firefox","title":"GitHub"}')
	s := string(out)
	// Extract JSON between single quotes.
	start := strings.Index(s, "'")
	end := strings.LastIndex(s, "'")
	if start < 0 || end <= start {
		return "", "", nil
	}
	jsonStr := s[start+1 : end]
	if jsonStr == "null" {
		return "", "", nil
	}

	var result struct {
		Class string `json:"class"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return "", "", nil
	}
	return result.Class, result.Title, nil
}

// --- sway/i3 backend (swaymsg/i3-msg) ---

func swayFocus(ctx context.Context) (string, string, error) {
	// Try swaymsg first, fall back to i3-msg.
	cmd := "swaymsg"
	if _, err := exec.LookPath("swaymsg"); err != nil {
		cmd = "i3-msg"
	}

	out, err := exec.CommandContext(ctx, cmd, "-t", "get_tree").Output()
	if err != nil {
		return "", "", err
	}

	class, title := findFocusedNode(out)
	return class, title, nil
}

func findFocusedNode(data []byte) (string, string) {
	return searchFocused(data)
}

func searchFocused(data []byte) (string, string) {
	var node struct {
		Focused bool              `json:"focused"`
		AppID   string            `json:"app_id"`
		Name    string            `json:"name"`
		Nodes   []json.RawMessage `json:"nodes"`
		Float   []json.RawMessage `json:"floating_nodes"`
	}
	if err := json.Unmarshal(data, &node); err != nil {
		return "", ""
	}
	if node.Focused && (node.AppID != "" || node.Name != "") {
		class := node.AppID
		if class == "" {
			class = node.Name
		}
		return class, node.Name
	}
	for _, child := range node.Nodes {
		if c, t := searchFocused(child); c != "" || t != "" {
			return c, t
		}
	}
	for _, child := range node.Float {
		if c, t := searchFocused(child); c != "" || t != "" {
			return c, t
		}
	}
	return "", ""
}

// --- KDE Wayland backend (kdotool or D-Bus) ---

func kdeFocus(ctx context.Context) (string, string, error) {
	// Try kdotool first.
	if _, err := exec.LookPath("kdotool"); err == nil {
		out, err := exec.CommandContext(ctx, "kdotool", "getactivewindow").Output()
		if err != nil {
			return "", "", err
		}
		winID := strings.TrimSpace(string(out))
		titleOut, _ := exec.CommandContext(ctx, "kdotool", "getactivewindow", "getname").Output()
		return winID, strings.TrimSpace(string(titleOut)), nil
	}

	// Fallback: KWin D-Bus.
	out, err := exec.CommandContext(ctx, "gdbus", "call", "--session",
		"--dest", "org.kde.KWin",
		"--object-path", "/KWin",
		"--method", "org.kde.KWin.getActiveWindow").Output()
	if err != nil {
		return "", "", err
	}
	return "", strings.TrimSpace(string(out)), nil
}
