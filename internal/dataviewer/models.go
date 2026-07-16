// Package dataviewer computes per-column profiling insights (null %, distinct %,
// distribution, quality/pattern badges) for a tabular preview, entirely in
// DuckDB over a bounded sample. It is UI-agnostic: it returns ColumnProfile
// values that the ui layer renders into insight headers. Profiling never
// iterates data rows in Go — all statistics come from SQL aggregates — with the
// sole exception of numeric histogram binning over the tiny already-fetched
// preview sample.
package dataviewer

import "github.com/apexion/apexion/internal/model"

// ColumnKind is the coarse family a DuckDB type falls into, which decides what
// statistics and distribution shape apply.
type ColumnKind int

const (
	KindOther ColumnKind = iota
	KindNumeric
	KindString
	KindTemporal
	KindBool
)

// ValueCount is one value and how often it occurs in the sample (top-N).
type ValueCount struct {
	Value string
	Count int64
}

// Bin is one histogram bucket for a numeric column.
type Bin struct {
	Label string
	Count int64
}

// Badge is a quality/pattern label shown on a column header. Tone is a coarse
// css intent: "info" (semantic/pattern), "warn" (quality risk), "muted".
type Badge struct {
	Label string
	Tone  string
}

// ColumnProfile is the UI-independent profile of a single column over the
// sample. Numeric fields are zero when not applicable to the column's Kind.
type ColumnProfile struct {
	Name string
	Type string // raw DuckDB type name
	Kind ColumnKind

	Count    int64 // total sampled rows (count(*))
	NonNull  int64 // count(col)
	Distinct int64 // approx_count_distinct(col)

	NullPct     float64 // (Count-NonNull)/Count
	DistinctPct float64 // Distinct/NonNull

	Min string
	Max string

	Mean   float64
	Std    float64
	Median float64

	MinLen int64
	MaxLen int64
	AvgLen float64

	TopValues []ValueCount // categorical distribution
	Histogram []Bin        // numeric distribution (binned in Go from the preview sample)

	Badges []Badge
}

// Source identifies the object to profile. It is reconstructable server-side
// (bucket/key/format/opts) so no client-supplied SQL is ever trusted.
type Source struct {
	Bucket string
	Key    string
	Format model.Format
	Opts   model.ReadOptions
}
