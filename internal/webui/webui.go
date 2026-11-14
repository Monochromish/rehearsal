// Package webui holds the built front end, embedded in the binary.
// The files in dist/ are produced by `npm run build` in web/.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built front end's files.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // cannot happen: "dist" is embedded above
	}
	return sub
}
