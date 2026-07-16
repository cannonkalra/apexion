// Package features is the single registration surface for every optional and
// core capability that plugs into Apexion at compile time.
//
// Implementations (file-format readers, table-format resolvers, storage
// providers, UI pages, previewers) register themselves from an init() function
// in a `register.go` file. Optional implementations gate that file — and their
// blank import in package internal/plugins — behind a Go build tag, so a
// capability that is not compiled leaves no trace in the binary: no code, no
// dependency, no route, no menu item.
//
// The rest of the application never imports a concrete implementation. It asks
// this package: features.ReaderRegistry(), features.Resolvers(), and so on.
// This inverts the dependency direction — implementations depend on the
// registry, never the other way round — which is what lets the crawler stay
// ignorant of csv/parquet/json/avro/...
//
// Registration happens during package initialisation (single-goroutine, before
// main), so the registries need no locking.
package features

import (
	"sort"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// ---- File-format readers (file-level plugins) ----------------------------

var readers = format.NewRegistry()

// RegisterReader adds a file-format reader. Called from a reader package's
// init(). Last registration for a given format wins.
func RegisterReader(r format.Reader) { readers.Register(r) }

// ReaderRegistry returns the shared reader registry the crawler samples files
// with. Callers must treat it as read-only.
func ReaderRegistry() *format.Registry { return readers }

// Reader returns the reader for a format, or nil if that format was not
// compiled in.
func Reader(f model.Format) format.Reader { return readers.Get(f) }

// ---- Table-format resolvers (dataset-level plugins) ----------------------

var resolvers = map[model.Format]format.TableResolver{}

// RegisterResolver adds a table-format resolver (Iceberg, Delta, ...). Called
// from a resolver package's init().
func RegisterResolver(r format.TableResolver) { resolvers[r.Format()] = r }

// Resolvers returns a copy of the registered table-format resolvers.
func Resolvers() map[model.Format]format.TableResolver {
	out := make(map[model.Format]format.TableResolver, len(resolvers))
	for f, r := range resolvers {
		out[f] = r
	}
	return out
}

// ---- Introspection -------------------------------------------------------

// Formats lists every file format and table format compiled into this binary,
// sorted. The Settings page uses this so it only ever advertises capabilities
// that are actually present — no toggles for absent modules.
func Formats() []string {
	seen := map[string]bool{}
	for _, f := range readers.Formats() {
		seen[string(f)] = true
	}
	for f := range resolvers {
		seen[string(f)] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
