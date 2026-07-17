// Package sqlgen turns a compatibility grouping into the SQL that opens the
// selected files as a virtual DuckDB table, plus a stable table name. It is
// pure: the DuckDB reader-syntax builder is injected (ReaderFunc), so this
// package imports no engine and its golden-SQL tests need no CGO. The selection
// service passes duckdb.ListReader as the ReaderFunc.
//
// Mixed formats cannot share one reader, so the selection is split by format
// upstream and Build produces one GeneratedTable per format group.
package sqlgen

import (
	"strings"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection/compat"
)

// ReaderFunc builds a DuckDB reader expression (e.g. read_parquet([...])) over a
// list of object URIs. duckdb.ListReader satisfies it.
type ReaderFunc func(uris []string, format model.Format, opts model.ReadOptions) (string, error)

// GeneratedTable is one virtual table: the SQL to open it, its name, the member
// files, and the merged schema (for the schema preview).
type GeneratedTable struct {
	Name   string
	Format model.Format
	Files  []compat.FileRef
	Merged []compat.MergedColumn
	SQL    string
}

// Build produces the SELECT that reads a grouping's member files as one table.
// reader turns the member URIs into the per-format read_* expression; opts are
// the live reader options from the SQL options drawer. Returns an empty SQL (no
// error) when the grouping has no members.
func Build(g compat.Grouping, format model.Format, opts model.ReadOptions, reader ReaderFunc) (GeneratedTable, error) {
	gt := GeneratedTable{Name: TableName(g.Members, format), Format: format, Files: g.Members, Merged: g.Merged}
	if len(g.Members) == 0 {
		return gt, nil
	}
	uris := make([]string, len(g.Members))
	for i, f := range g.Members {
		uris[i] = f.URI()
	}
	from, err := reader(uris, format, opts)
	if err != nil {
		return gt, err
	}
	gt.SQL = "SELECT * FROM " + from
	return gt, nil
}

// TableName derives a stable, valid virtual-table identifier from the members'
// common key prefix (falling back to the format name), suffixed with the file
// count so different selections read distinctly. Always a valid SQL identifier.
func TableName(members []compat.FileRef, format model.Format) string {
	base := commonBase(members)
	if base == "" {
		base = string(format)
	}
	name := sanitizeIdent(base)
	if name == "" {
		name = "selected"
	}
	return name
}

// commonBase returns the last path segment of the members' longest common
// directory prefix, e.g. keys under "data/orders/" → "orders".
func commonBase(members []compat.FileRef) string {
	if len(members) == 0 {
		return ""
	}
	dirs := make([]string, len(members))
	for i, m := range members {
		dirs[i] = dirOf(m.Key)
	}
	prefix := dirs[0]
	for _, d := range dirs[1:] {
		prefix = commonPrefix(prefix, d)
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		// No shared directory: use the first file's stem.
		return stem(baseName(members[0].Key))
	}
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		return prefix[i+1:]
	}
	return prefix
}

func dirOf(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[:i]
	}
	return ""
}

func baseName(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func stem(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

func commonPrefix(a, b string) string {
	n := min(len(b), len(a))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// sanitizeIdent lowercases and maps any non [a-z0-9_] rune to '_', ensuring the
// result starts with a letter or underscore (matching duckdb.ValidIdentifier).
func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if c := out[0]; c >= '0' && c <= '9' {
		out = "t_" + out
	}
	return out
}
