package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var content embed.FS

func Files() fs.FS {
	f, err := fs.Sub(content, "assets")
	if err != nil {
		panic(err)
	}
	return f
}
