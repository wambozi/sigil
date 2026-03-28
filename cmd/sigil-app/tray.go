package main

import (
	"fmt"

	"github.com/getlantern/systray"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Minimal 16x16 pixel PNG placeholder icon (opaque white square).
// In production this would be replaced with the actual Sigil logo.
var iconConnected = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x10, // 16x16
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0xf3, 0xff, // RGBA
	0x61, 0x00, 0x00, 0x00, 0x01, 0x73, 0x52, 0x47, // sRGB chunk
	0x42, 0x00, 0xae, 0xce, 0x1c, 0xe9, 0x00, 0x00,
	0x00, 0x44, 0x49, 0x44, 0x41, 0x54, 0x38, 0x4f,
	0x63, 0x64, 0x60, 0x60, 0xf8, 0x0f, 0x00, 0x01,
	0x01, 0x01, 0x00, 0x18, 0xd8, 0x60, 0x84, 0x02,
	0x46, 0x28, 0x1b, 0xc4, 0x60, 0x82, 0x32, 0x40,
	0x0c, 0x26, 0x28, 0x03, 0x44, 0x60, 0x02, 0x51,
	0x30, 0x1a, 0xa0, 0x0c, 0x10, 0x83, 0x19, 0x44,
	0x01, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0x03,
	0x00, 0x06, 0x10, 0x00, 0x01, 0xd7, 0x9d, 0xc4,
	0x3e, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

// iconDisconnected is a dimmed variant of the icon for disconnected state.
// In production this would be a separate greyed-out Sigil logo.
var iconDisconnected = iconConnected

var (
	mStatus *systray.MenuItem
	mPause  *systray.MenuItem
	paused  bool
)

// setupTray initialises the system tray icon and menu. It blocks until the tray
// is ready, so it should be called from a goroutine.
func setupTray(app *App) {
	systray.Run(func() {
		onTrayReady(app)
	}, func() {
		// Cleanup on exit.
	})
}

func onTrayReady(app *App) {
	systray.SetTemplateIcon(iconConnected, iconConnected)
	systray.SetTooltip("Sigil")

	mOpen := systray.AddMenuItem("Open Sigil", "Show suggestion viewer")
	mStatus = systray.AddMenuItem("Status: Connecting...", "")
	mStatus.Disable()

	systray.AddSeparator()

	mLevel := systray.AddMenuItem("Notification Level", "")
	levels := []string{"Silent", "Digest", "Ambient", "Conversational", "Autonomous"}
	levelItems := make([]*systray.MenuItem, 5)
	for i, name := range levels {
		levelItems[i] = mLevel.AddSubMenuItem(fmt.Sprintf("%d - %s", i, name), "")
	}

	systray.AddSeparator()
	mPause = systray.AddMenuItem("Pause Suggestions", "")
	mQuit := systray.AddMenuItem("Quit Sigil App", "")

	// Handle menu clicks in a goroutine.
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if app.ctx != nil {
					wailsrt.WindowShow(app.ctx)
				}
			case <-levelItems[0].ClickedCh:
				_ = app.SetLevel(0)
			case <-levelItems[1].ClickedCh:
				_ = app.SetLevel(1)
			case <-levelItems[2].ClickedCh:
				_ = app.SetLevel(2)
			case <-levelItems[3].ClickedCh:
				_ = app.SetLevel(3)
			case <-levelItems[4].ClickedCh:
				_ = app.SetLevel(4)
			case <-mPause.ClickedCh:
				paused = !paused
				if paused {
					mPause.SetTitle("Resume Suggestions")
				} else {
					mPause.SetTitle("Pause Suggestions")
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				if app.ctx != nil {
					wailsrt.Quit(app.ctx)
				}
			}
		}
	}()
}

// updateTrayStatus swaps the tray icon and status label based on connection
// state.
func updateTrayStatus(connected bool) {
	if mStatus == nil {
		return
	}
	if connected {
		systray.SetTemplateIcon(iconConnected, iconConnected)
		mStatus.SetTitle("Status: Connected")
	} else {
		systray.SetTemplateIcon(iconDisconnected, iconDisconnected)
		mStatus.SetTitle("Status: Disconnected")
	}
}
