// Package web embeds the built React SPA for serving from the panel binary.
package web

import "embed"

// Dist holds the Vite build output under dist/.
//
//go:embed all:dist
var Dist embed.FS
