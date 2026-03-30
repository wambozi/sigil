package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:             "Sigil",
		Width:             480,
		Height:            680,
		MinWidth:          400,
		MinHeight:         480,
		Assets:            assets,
		StartHidden:       false,
		HideWindowOnClose: true,
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		Bind:              []interface{}{app},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "Sigil",
				Message: "Workflow intelligence for software engineers",
			},
		},
	})

	if err != nil {
		panic(err)
	}
}
