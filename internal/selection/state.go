// Package selection is the orchestrator for the multi-file "smart preview"
// feature: it takes a set of files a user selected in the Explorer, discovers
// each file's schema from object storage (no full scans — DuckDB DESCRIBE reads
// only Parquet footers / bounded CSV-JSON samples), groups them by
// compatibility, and generates the SQL that opens the compatible set as one
// virtual DuckDB table.
//
// It is the only package in this feature that touches the engine; the actual
// compatibility rules (internal/selection/compat) and SQL generation
// (internal/selection/sqlgen) are pure and live in subpackages. Discovery runs
// in the background per selection, is cancellable, and is replaced wholesale
// when the selection changes.
package selection

import (
	"context"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection/compat"
	"github.com/apexion/apexion/internal/selection/sqlgen"
)

// FileRef aliases the lowest-layer file identity so callers use one type.
type FileRef = compat.FileRef

// Describer is the slice of the DuckDB engine the service needs. *duckdb.Engine
// satisfies it; tests inject a fake so no live database is required.
type Describer interface {
	Ready() bool
	DescribeFile(ctx context.Context, bucket, key string, format model.Format, opts model.ReadOptions) ([]duckdb.ColumnDef, error)
	ParquetRowCount(ctx context.Context, uri string) (int64, error)
}

// Status is a file's schema-discovery progress state.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusDone       Status = "done"
	StatusError      Status = "error"
)

// Verdict is a file's compatibility outcome, determined after grouping.
type Verdict string

const (
	VerdictNone         Verdict = ""             // not yet determined
	VerdictCompatible   Verdict = "compatible"   // ✓
	VerdictWarning      Verdict = "widened"      // ⚠ widened types
	VerdictIncompatible Verdict = "incompatible" // ✕
)

// FileState is the live per-file state within a selection session.
type FileState struct {
	Ref     FileRef         `json:"ref"`
	Status  Status          `json:"status"`
	Verdict Verdict         `json:"verdict"`
	Cols    []compat.Column `json:"-"`
	Err     string          `json:"err,omitempty"`
}

// Key is the stable identity of a selected file (matches the UI's selKey).
func (f FileState) Key() string {
	return f.Ref.Bucket + " " + f.Ref.Key + " " + string(f.Ref.Format)
}

// CompatSummary is the verdict for a completed selection: the generated virtual
// table(s), the merged schema of the primary table, warning/error reasons, and
// the aggregate counts shown in the summary panel.
type CompatSummary struct {
	Tables      []sqlgen.GeneratedTable `json:"tables"`
	Merged      []compat.MergedColumn   `json:"merged"` // primary table's schema (preview)
	Warnings    []compat.Reason         `json:"warnings"`
	Rejected    []compat.Reason         `json:"rejected"`
	Formats     []model.Format          `json:"formats"`
	MixedFormat bool                    `json:"mixed_format"`
	CompatCount int                     `json:"compat_count"` // ✓ files
	WarnCount   int                     `json:"warn_count"`   // ⚠ files
	ErrorCount  int                     `json:"error_count"`  // ✕ files
	ColumnCount int                     `json:"column_count"`
	TotalSize   int64                   `json:"total_size"`
	RowsEst     int64                   `json:"rows_est"` // -1 when unknown
	Options     model.ReadOptions       `json:"options"`  // reader options the SQL was built with
}

// PrimaryTable returns the virtual table with the most member files (the one the
// SQL editor prefills), or nil when there are none.
func (s *CompatSummary) PrimaryTable() *sqlgen.GeneratedTable {
	if s == nil || len(s.Tables) == 0 {
		return nil
	}
	best := 0
	for i := range s.Tables {
		if len(s.Tables[i].Files) > len(s.Tables[best].Files) {
			best = i
		}
	}
	return &s.Tables[best]
}

// ProgressSnapshot is an immutable copy of a session's state for the UI. While
// Complete is false the UI keeps polling; Summary is set once Complete is true.
type ProgressSnapshot struct {
	Token    string         `json:"token"`
	Total    int            `json:"total"`
	Done     int            `json:"done"`
	Errored  int            `json:"errored"`
	Fraction float64        `json:"fraction"`
	Complete bool           `json:"complete"`
	Files    []FileState    `json:"files"`
	Summary  *CompatSummary `json:"summary,omitempty"`
}
