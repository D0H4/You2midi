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
		Title:            "You2Midi",
		Width:            1100,
		Height:           760,
		MinWidth:         960,
		MinHeight:        640,
		DisableResize:    false,
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
	})
	if err != nil {
		log.Fatal(err)
	}
}
