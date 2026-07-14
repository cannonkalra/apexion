// Package format defines the pluggable file-format reader abstraction used by
// the crawler and inference engine.
//
// A Reader extracts a schema (and a small sample of rows) from a single
// object. New formats plug in by implementing Reader and registering with a
// Registry — this is the extension point that lets Apexion grow to support new
// storage formats without touching the crawler.
package format

import (
	"context"
	"io"

	"github.com/apexion/apexion/internal/model"
)

// Field is a single physical column discovered in a file.
type Field struct {
	Name         string         `json:"name"`
	Type         model.DataType `json:"type"`
	PhysicalType string         `json:"physical_type"`
	Nullable     bool           `json:"nullable"`
}

// Result is the outcome of reading one object's schema and sample.
type Result struct {
	Format           model.Format      `json:"format"`
	Compression      model.Compression `json:"compression"`
	Fields           []Field           `json:"fields"`
	Rows             [][]string        `json:"rows"` // sample rows, cells aligned to Fields
	RowCountEstimate int64             `json:"row_count_estimate"`
	PartitionKeys    []string          `json:"partition_keys"` // table-format partition columns
}

// Entry is a single listed object under a prefix.
type Entry struct {
	Key  string
	Size int64
}

// Catalog provides directory-style access to an object store, used by the
// table-format resolvers (Iceberg, Delta) that must read metadata files.
type Catalog interface {
	// List returns objects whose key begins with prefix.
	List(ctx context.Context, prefix string) ([]Entry, error)
	// Get fetches the full bytes of a (small) metadata object.
	Get(ctx context.Context, key string) ([]byte, error)
}

// TableResolver detects and describes a table-format dataset rooted at a
// prefix (e.g. an Iceberg or Delta table). It is a dataset-level plugin, as
// opposed to Reader which is a file-level plugin.
type TableResolver interface {
	Format() model.Format
	// Detect reports whether prefix roots a table of this format and, if so,
	// returns its resolved schema. ok is false when it is not such a table.
	Detect(ctx context.Context, cat Catalog, prefix string) (result *Result, ok bool, err error)
}

// Options tune how much a reader samples.
type Options struct {
	SampleRows  int
	SampleBytes int64
	Delimiter   rune // csv/tsv
	HasHeader   bool
}

// DefaultOptions returns sensible sampling defaults.
func DefaultOptions() Options {
	return Options{SampleRows: 1000, SampleBytes: 262144, Delimiter: ',', HasHeader: true}
}

// Source abstracts access to a single object's bytes. The crawler backs this
// with a MinIO object (which supports both streaming and random access); tests
// back it with in-memory buffers.
type Source interface {
	// Key is the object key (used for extension hints).
	Key() string
	// Size is the object size in bytes.
	Size() int64
	// Open returns a fresh streaming reader positioned at the start.
	Open(ctx context.Context) (io.ReadCloser, error)
	// ReaderAt returns a random-access view for footer-based formats
	// (Parquet/ORC). Implementations may return the same underlying handle.
	ReaderAt(ctx context.Context) (io.ReaderAt, int64, error)
}

// Reader extracts schema + sample from a Source for one format.
type Reader interface {
	Format() model.Format
	ReadSchema(ctx context.Context, src Source, opts Options) (*Result, error)
}

// Registry maps formats to readers. It is the plugin surface for formats.
type Registry struct {
	readers map[model.Format]Reader
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{readers: map[model.Format]Reader{}} }

// Register adds a reader (last registration for a format wins).
func (r *Registry) Register(rd Reader) { r.readers[rd.Format()] = rd }

// Get returns the reader for a format, or nil.
func (r *Registry) Get(f model.Format) Reader { return r.readers[f] }

// Formats lists the registered formats.
func (r *Registry) Formats() []model.Format {
	out := make([]model.Format, 0, len(r.readers))
	for f := range r.readers {
		out = append(out, f)
	}
	return out
}
