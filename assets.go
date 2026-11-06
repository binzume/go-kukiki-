package main

import (
	"embed"
	"io/fs"
	"os"
)

// embeddedStatic contains the default templates and static assets used by
// the standalone binary.
//
//go:embed static
var embeddedStatic embed.FS

func (c *Config) templateFS() (fs.FS, error) {
	if c.TemplateDir == "" {
		return fs.Sub(embeddedStatic, "static")
	}
	return os.DirFS(c.TemplateDir), nil
}
