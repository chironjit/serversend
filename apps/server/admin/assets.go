// Package admin embeds the compiled public administration shell. Build it with
// npm ci && npm run build in this directory before compiling the Go server.
package admin

import "embed"

// Files contains production assets only, never source files or dependencies.
//
//go:embed dist
var Files embed.FS
