package main

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed static/index.html
var staticFiles embed.FS

// staticFS is the embedded filesystem containing static assets.
// It is a sub-filesystem rooted at "static/" with a single file: index.html.
var staticFS = func() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}
	return sub
}()
