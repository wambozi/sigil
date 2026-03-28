package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "Sigil",
		Width:            420,
		Height:           680,
		MinWidth:         360,
		MinHeight:        480,
		Assets:           assets,
		StartHidden:      true,
		HideWindowOnClose: true,
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
		Linux: &linux.Options{
			WindowIsTranslucent: true,
		},
	})

	if err != nil {
		panic(err)
	}
}
