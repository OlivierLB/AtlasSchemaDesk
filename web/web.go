// Package web embeds the built React interface (frontend/ → web/dist/app).
package web

import (
	"embed"
	"io/fs"
)

// dist always contains README.md so the package compiles before the first
// frontend build; Vite only empties dist/app.
//
//go:embed all:dist
var dist embed.FS

// FS returns the built interface (index.html, assets/), or nil when the
// frontend has not been built into this binary.
func FS() fs.FS {
	app, err := fs.Sub(dist, "dist/app")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(app, "index.html"); err != nil {
		return nil
	}
	return app
}
