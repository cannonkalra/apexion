// Package assets embeds the compiled static assets (Tailwind CSS, HTMX,
// images) so the binary is fully self-contained.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed css/app.css js/htmx.min.js img/favicon.svg
var files embed.FS

// FS returns the embedded asset filesystem rooted so that paths look like
// "css/app.css", suitable for serving under /assets/.
func FS() fs.FS { return files }
