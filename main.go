package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "AWS Session Manager",
		Width:     1200,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "173a482a-a4d8-4911-9e01-66f015401cf1",
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		BackgroundColour:  &options.RGBA{R: 24, G: 26, B: 32, A: 1},
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		OnBeforeClose:     app.beforeClose,
		HideWindowOnClose: false,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
}
