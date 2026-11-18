// Package web embeds the built UI (npm run build writes dist/).
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// FS is the UI rooted at dist/.
func FS() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
