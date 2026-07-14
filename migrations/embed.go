// Package migrations embeds the SQL migration files so they ship inside the
// binary and are also available on disk for inspection.
package migrations

import "embed"

// FS holds every *.sql migration, applied in lexical filename order.
//
//go:embed *.sql
var FS embed.FS
