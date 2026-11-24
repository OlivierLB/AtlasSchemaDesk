//go:build desktop

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"

	"github.com/OlivierLB/Sulcata/packaging"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// desktopBuild is true: launching without a subcommand opens a native window.
const desktopBuild = true

// loaderPage is the window's first page; it hands over to the local server so
// the interface runs with exactly the same HTTP behaviour as in a browser.
const loaderPage = `<!doctype html><html lang="fr"><head><meta charset="utf-8">
<title>Sulcata</title><style>body{margin:0;display:grid;place-items:center;height:100vh;
background:#111821;color:#9fb0c3;font:14px system-ui,sans-serif}</style></head>
<body>Chargement de Sulcata…<script>location.replace(%q)</script></body></html>`

// runWindow shows the interface served at url in a native window. It returns
// when the window is closed, or closes it when quit is closed.
func runWindow(url string, quit <-chan struct{}) error {
	// WebKitGTK's DMA-BUF renderer shows a blank window with several GPU
	// drivers (NVIDIA notably, also some VMs); the fallback renderer works
	// everywhere. A value set by the user is kept.
	if runtime.GOOS == "linux" && os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
	page := fmt.Sprintf(loaderPage, url) // %q yields a valid JS string for this ASCII URL
	return wails.Run(&options.App{
		Title:            "Sulcata",
		Width:            1440,
		Height:           900,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 17, G: 24, B: 33, A: 255},
		AssetServer: &assetserver.Options{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		})},
		OnStartup: func(ctx context.Context) {
			if quit != nil {
				go func() {
					<-quit
					wailsruntime.Quit(ctx)
				}()
			}
		},
		Windows: &windows.Options{},
		Linux:   &linux.Options{ProgramName: "sulcata", Icon: packaging.Icon},
	})
}
