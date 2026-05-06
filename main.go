package main

import (
	"embed"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// localFileMiddleware lets the frontend load local files (video preview)
// through the Wails asset server so WebView2 security doesn't block them.
func localFileMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/localfile" {
			filePath := r.URL.Query().Get("path")
			if filePath != "" {
				http.ServeFile(w, r, filePath)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Steam Showcase Maker",
		Width:     1280,
		Height:    820,
		MinWidth:  800,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: assetserver.Middleware(localFileMiddleware),
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
