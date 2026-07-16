package ui

import (
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/catalog/virtualpath"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
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
