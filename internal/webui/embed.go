// Package webui embeds the built SvelteKit UI. The Docker build copies the
// SvelteKit output into ./dist before `go build`; a stub index.html is kept in
// the repo so the module always compiles even without a UI build.
package webui

import "embed"

//go:embed all:dist
var Assets embed.FS
