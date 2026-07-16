package model

import "time"

// Refresh modes and schema strategies for a catalog entry.
const (
	RefreshManual  = "manual"   // refreshed only on explicit request
	RefreshOnQuery = "on_query" // view recreated before each query (always fresh)

	SchemaUnion  = "union"  // union_by_name across compatible files (default)
	SchemaStrict = "strict" // require identical schemas
	SchemaLatest = "latest" // use the newest file's schema
)

// CatalogEntry is a logical SQL table registered from a discovered dataset. It
// maps a table name to a dataset's storage location; DuckDB exposes it as a
// view that reads directly from object storage — no data is ingested or copied.
//
// A CatalogEntry is not a Dataset: the Dataset is discovered metadata, the
// CatalogEntry is the user's decision to expose that dataset as SQL.
type CatalogEntry struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"` // the SQL table name (unique)
	DatasetID      string     `json:"dataset_id"`
	BucketName     string     `json:"bucket_name"`
	RootPath       string     `json:"root_path"`
	Format         Format     `json:"format"`
	URI            string     `json:"uri"`
	Glob           string     `json:"glob"`
	Enabled        bool       `json:"enabled"`
	RefreshMode    string     `json:"refresh_mode"`
	SchemaStrategy string     `json:"schema_strategy"`
	PartitionCols  []string   `json:"partition_cols"`
	Description    string     `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastRefreshAt  *time.Time `json:"last_refresh_at,omitempty"`
}
