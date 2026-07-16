package ui

import (
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

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
}

// DatasetsVM is the datasets listing view model.
type DatasetsVM struct {
	Datasets []model.Dataset
	Buckets  []model.Bucket
	Filter   storage.DatasetFilter
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
