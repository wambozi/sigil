//go:build windows

package sources

import (
	"context"
	"log/slog"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

var (
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	enumClipboardFormats       = user32.NewProc("EnumClipboardFormats")
	getClipboardData           = user32.NewProc("GetClipboardData")
	globalSize                 = kernel32.NewProc("GlobalSize")
)

// WindowsClipboardSource monitors clipboard changes on Windows by polling
// GetClipboardSequenceNumber. The sequence number increments on every
// clipboard change system-wide, so we only emit an event when it advances.
type WindowsClipboardSource struct {
	log       *slog.Logger
	lastSeqNo uint32
}

// NewWindowsClipboardSource creates a WindowsClipboardSource.
func NewWindowsClipboardSource(log *slog.Logger) *WindowsClipboardSource {
	return &WindowsClipboardSource{log: log}
}

func (s *WindowsClipboardSource) Name() string { return "clipboard" }

func (s *WindowsClipboardSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		// Initialize sequence number so we don't fire on startup.
		seqNo, _, _ := getClipboardSequenceNumber.Call()
		s.lastSeqNo = uint32(seqNo)
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

func (s *WindowsClipboardSource) poll(ch chan<- event.Event) {
	seqNo, _, _ := getClipboardSequenceNumber.Call()
	if uint32(seqNo) == s.lastSeqNo {
		return
	}
	s.lastSeqNo = uint32(seqNo)

	contentType, contentLength := getClipboardInfo()

	ev := event.Event{
		Kind:   event.KindClipboard,
		Source: "clipboard",
		Payload: map[string]any{
			"source_app":     "",
			"content_type":   contentType,
			"content_length": contentLength,
		},
		Timestamp: time.Now(),
	}
	select {
	case ch <- ev:
	default:
	}
}

// Clipboard format constants.
const (
	cfText        = 1
	cfBitmap      = 2
	cfUnicodeText = 13
	cfHDrop       = 15
)

func getClipboardInfo() (string, int) {
	ret, _, _ := openClipboard.Call(0)
	if ret == 0 {
		return "unknown", 0
	}
	defer closeClipboard.Call()

	format, _, _ := enumClipboardFormats.Call(0)
	if format == 0 {
		return "unknown", 0
	}

	var contentType string
	switch uint32(format) {
	case cfText, cfUnicodeText:
		contentType = "text/plain"
	case cfBitmap:
		contentType = "image/bitmap"
	case cfHDrop:
		contentType = "application/x-file-list"
	default:
		contentType = "application/octet-stream"
	}

	data, _, _ := getClipboardData.Call(format)
	if data == 0 {
		return contentType, 0
	}
	size, _, _ := globalSize.Call(data)
	return contentType, int(size)
}
