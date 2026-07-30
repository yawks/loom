package main

import (
	"embed"

	"context"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()
	app.mockMode = slices.Contains(os.Args[1:], "--mock")

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "Loom",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnDomReady: func(ctx context.Context) {
			app.domReady(ctx)
			setWindowCornerRadius(6)
		},
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		// System tray configuration
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		Windows: &windows.Options{
			// Windows-specific options
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
