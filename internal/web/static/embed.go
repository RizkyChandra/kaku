// Package static holds the admin UI's client assets, compiled into the binary.
package static

import "embed"

//go:embed *.css *.js
var FS embed.FS
