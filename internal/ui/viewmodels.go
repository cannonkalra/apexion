package ui

import (
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/catalog/virtualpath"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection"
	"github.com/apexion/apexion/internal/storage"
)

// WizardVM drives the unified Crawl → Analyze → Review → Register wizard.
type WizardVM struct {
	Bucket    string
	Prefix    string
	JobID     string
	Job       *model.Job
	Detail    *catalog.DatasetDetail
	Suggested string // suggested SQL table name
	Entry     *model.CatalogEntry
	Error     string
}

// ExplorerVM powers the VS Code-style bucket/folder/file browser.
type ExplorerVM struct {
	Buckets      []string // union of server-listed + cataloged + selected buckets
	Bucket       string
	Listing      *explorer.DirListing
	Crumbs       []explorer.Crumb
	Error        string
	Connections  []model.Connection
	ActiveConn   *model.Connection
	ServerListOK bool // false when ListAllMyBuckets is denied/unavailable
	// Infinite selects the load-more UX: false (default) renders a "Load more"
	// button; true swaps in the next page automatically when the sentinel scrolls
	// into view (hx-trigger="revealed"). Threaded into DirListing so the fragment
	// route can reproduce the same trigger for appended sentinels.
	Infinite bool
	// TotalBuckets is the full number of buckets known before capping. When it
	// exceeds len(Buckets), the sidebar shows a "Load more buckets" control.
	TotalBuckets int
}

// PreviewVM powers the instant file preview page.
type PreviewVM struct {
	Bucket      string
	Key         string
	Name        string
	Format      model.Format
	Compression model.Compression
	Size        int64
	Limit       int
	Result      *duckdb.Result
	Error       string
	Ready       bool
	Partitions  []virtualpath.Partition // virtual Hive partitions inferred from the path
	VirtualPath string                  // pt0=…/pt1=…/file
	Opts        model.ReadOptions       // reader options the preview was run with
}

// PreviewOptsVM drives a live reader-options bar shown above a data preview. On
// change it re-submits Endpoint (with the toggles + any Hidden identity fields)
// and swaps the regenerated SQL + result table into #Target.
type PreviewOptsVM struct {
	Endpoint string
	Method   string // "get" (default) or "post"
	Target   string
	Swap     string // hx-swap value; default "innerHTML"
	Format   model.Format
	Opts     model.ReadOptions
	// ShowFormat renders a "Read as" format selector so a misdetected or
	// unknown-extension file (e.g. a gzipped log with no .jsonl suffix) can be
	// coerced into a supported reader. Off for dataset previews (fixed format).
	ShowFormat bool
	Hidden     [][2]string
}

// QueryFileVM identifies a single object opened in the SQL editor via
// "Query in SQL". It backs the editor's reader-options bar, which regenerates
// the FROM clause (and thus the prefilled SQL) when the format or a toggle
// changes — the single-file analogue of the multi-file selection drawer.
type QueryFileVM struct {
	Bucket string
	Key    string
	Format model.Format
	Opts   model.ReadOptions
}

// DatasetsVM is the datasets listing view model.
type DatasetsVM struct {
	Datasets   []model.Dataset
	Buckets    []model.Bucket
	Filter     storage.DatasetFilter
	Registered map[string]int // dataset id → number of catalog tables
}

// CatalogVM powers the catalog (logical SQL tables) page.
type CatalogVM struct {
	Entries []model.CatalogEntry
}

// QueryVM powers the SQL query console over catalog tables.
type QueryVM struct {
	Tables     []model.CatalogEntry
	Selected   string
	InitialSQL string
	Ready      bool

	// Selection-backed editor (multi-file smart preview). Sel is the selection
	// token; when set, the editor shows the options drawer + schema preview.
	Sel        string
	SelSummary *selection.CompatSummary
	SelOpts    model.ReadOptions
	SelExpired bool

	// Single-file editor ("Query in SQL" from a file preview). When set, the
	// editor shows a reader-options bar (format override + toggles) that
	// regenerates InitialSQL on change.
	File *QueryFileVM
}

// SettingsVM powers the settings page.
type SettingsVM struct {
	Connections  []model.Connection
	ActiveConnID string
	StoragePath  string
	Workers      int
	Buckets      []model.Bucket
}

func toastTone(tone string) string {
	switch tone {
	case "error":
		return "border-accent-rose"
	case "info":
		return "border-brand-500"
	default:
		return "border-accent-emerald"
	}
}
