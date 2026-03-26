//go:build windows

package sources

import (
	"context"
	"log/slog"
	"syscall"
	"time"
	"unsafe"

	"github.com/wambozi/sigil/internal/event"
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	getForegroundWindow      = user32.NewProc("GetForegroundWindow")
	getWindowTextW           = user32.NewProc("GetWindowTextW")
	getWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	getWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")

	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	openProcess                = kernel32.NewProc("OpenProcess")
	queryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	closeHandle                = kernel32.NewProc("CloseHandle")
)

// WindowsFocusSource detects active window changes on Windows via polling
// GetForegroundWindow. A push-based approach using SetWinEventHook would
// require a message pump, so we poll at 1-second intervals instead.
type WindowsFocusSource struct {
	log     *slog.Logger
	lastKey string
}

// NewWindowsFocusSource creates a WindowsFocusSource.
func NewWindowsFocusSource(log *slog.Logger) *WindowsFocusSource {
	return &WindowsFocusSource{log: log}
}

func (s *WindowsFocusSource) Name() string { return "windows-focus" }

func (s *WindowsFocusSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(1 * time.Second)
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

func (s *WindowsFocusSource) poll(ch chan<- event.Event) {
	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return
	}

	title := getWindowTitle(hwnd)
	procName := getProcessName(hwnd)

	key := procName + "|" + title
	if key == s.lastKey {
		return
	}
	s.lastKey = key

	ev := event.Event{
		Kind:   event.KindHyprland,
		Source: "windows-focus",
		Payload: map[string]any{
			"window_class": procName,
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

func getWindowTitle(hwnd uintptr) string {
	length, _, _ := getWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return syscall.UTF16ToString(buf)
}

func getProcessName(hwnd uintptr) string {
	var pid uint32
	getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	const processQueryLimitedInformation = 0x1000
	h, _, _ := openProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer closeHandle.Call(h)

	var size uint32 = 260
	buf := make([]uint16, size)
	queryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	fullPath := syscall.UTF16ToString(buf[:size])

	// Extract just the executable name.
	for i := len(fullPath) - 1; i >= 0; i-- {
		if fullPath[i] == '\\' {
			return fullPath[i+1:]
		}
	}
	return fullPath
}
