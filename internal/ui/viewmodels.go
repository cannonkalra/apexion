package ui

import (
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/preview"
	"github.com/apexion/apexion/internal/storage"
)

// ExplorerVM powers the VS Code-style bucket/folder/file browser.
type ExplorerVM struct {
	Buckets []string // all buckets on the server
	Bucket  string
	Listing *explorer.DirListing
	Crumbs  []explorer.Crumb
	Error   string
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
	Result      *preview.Result
	Error       string
	Ready       bool
}

// SQLVM powers the SQL scratchpad.
type SQLVM struct {
	DatasetID   string
	DatasetName string
	InitialSQL  string
	Ready       bool
	SetupError  string
}

// JobsVM powers the crawl jobs page.
type JobsVM struct {
	Jobs []model.Job
}

// SearchVM is the search result view model (used by both the topbar dropdown
// and the full search page).
type SearchVM struct {
	Query    string
	Datasets []model.Dataset
	Columns  []storage.ColumnHit
	Buckets  []model.Bucket
}

// Empty reports whether there are no results.
func (s SearchVM) Empty() bool {
	return len(s.Datasets) == 0 && len(s.Columns) == 0 && len(s.Buckets) == 0
}

// Total counts all hits.
func (s SearchVM) Total() int { return len(s.Datasets) + len(s.Columns) + len(s.Buckets) }

// BucketsVM lists every bucket on the connected server (via S3 ListBuckets),
// merged with catalog state so the page shows both connected and available
// buckets.
type BucketsVM struct {
	Endpoint    string
	ServerOK    bool   // did ListBuckets succeed?
	ServerError string // populated when ListBuckets failed
	Rows        []BucketRow
}

// BucketRow is one bucket on the page — either already cataloged (connected) or
// merely available on the server (not yet crawled).
type BucketRow struct {
	Name      string
	Cataloged bool
	Bucket    model.Bucket
	Datasets  []model.Dataset
}

// Connected counts cataloged buckets.
func (vm BucketsVM) Connected() int {
	n := 0
	for _, r := range vm.Rows {
		if r.Cataloged {
			n++
		}
	}
	return n
}

// Available counts server buckets not yet crawled.
func (vm BucketsVM) Available() int { return len(vm.Rows) - vm.Connected() }

// DatasetsVM is the datasets listing view model.
type DatasetsVM struct {
	Datasets []model.Dataset
	Buckets  []model.Bucket
	Filter   storage.DatasetFilter
}

// SchemaVM powers the schema explorer.
type SchemaVM struct {
	Datasets []model.Dataset
	Selected *catalog.DatasetDetail
}

// InferenceVM powers the inference page.
type InferenceVM struct {
	Datasets []model.Dataset
	Selected *catalog.DatasetDetail
	Runs     []model.InferenceRun
}

// RunsVM powers the crawler runs page.
type RunsVM struct {
	Runs []model.CrawlerRun
}

// LineageVM powers the lineage page.
type LineageVM struct {
	Nodes  []model.LineageNode
	Edges  []model.LineageEdge
	Groups []lineageGroup
}

type lineageGroup struct {
	Kind  string
	Label string
	Nodes []model.LineageNode
}

// SettingsVM powers the settings page.
type SettingsVM struct {
	MinIOEndpoint string
	MinIORegion   string
	StoragePath   string
	Workers       int
	AgentProvider string
	AgentModel    string
	Agents        []AgentInfoVM
	Buckets       []model.Bucket
}

// AgentInfoVM is a UI-facing agent summary.
type AgentInfoVM struct {
	Name     string
	Kind     string
	Provider string
	Enabled  bool
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
