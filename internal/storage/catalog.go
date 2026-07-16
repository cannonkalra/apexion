package storage

import (
	"context"
	"database/sql"

	"github.com/apexion/apexion/internal/model"
)

const catalogSelect = `SELECT id, name, dataset_id, bucket_name, root_path, format,
	uri, glob_pattern, enabled, refresh_mode, schema_strategy, partition_cols, read_options, description,
	created_at, updated_at, last_refresh_at FROM catalog_entries`

// UpsertCatalogEntry inserts or updates a catalog entry (by id).
func (s *Store) UpsertCatalogEntry(ctx context.Context, e *model.CatalogEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO catalog_entries (id, name, dataset_id, bucket_name, root_path, format,
			uri, glob_pattern, enabled, refresh_mode, schema_strategy, partition_cols, read_options, description,
			created_at, updated_at, last_refresh_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, bucket_name=excluded.bucket_name, root_path=excluded.root_path,
			format=excluded.format, uri=excluded.uri, glob_pattern=excluded.glob_pattern, enabled=excluded.enabled,
			refresh_mode=excluded.refresh_mode, schema_strategy=excluded.schema_strategy,
			partition_cols=excluded.partition_cols, read_options=excluded.read_options, description=excluded.description,
			updated_at=excluded.updated_at, last_refresh_at=excluded.last_refresh_at`,
		e.ID, e.Name, e.DatasetID, e.BucketName, e.RootPath, string(e.Format),
		e.URI, e.Glob, e.Enabled, e.RefreshMode, e.SchemaStrategy, toJSON(e.PartitionCols), toJSON(e.ReadOptions),
		e.Description, e.CreatedAt, e.UpdatedAt, e.LastRefreshAt)
	return err
}

// ListCatalogEntries returns all catalog entries ordered by name.
func (s *Store) ListCatalogEntries(ctx context.Context) ([]model.CatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx, catalogSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CatalogEntry
	for rows.Next() {
		e, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetCatalogEntry returns a catalog entry by id (nil if absent).
func (s *Store) GetCatalogEntry(ctx context.Context, id string) (*model.CatalogEntry, error) {
	return scanCatalogEntry(s.db.QueryRowContext(ctx, catalogSelect+` WHERE id = ?`, id))
}

// ListCatalogEntriesByDataset returns the catalog entries backed by a dataset.
// A dataset may back several tables (different globs/strategies/names).
func (s *Store) ListCatalogEntriesByDataset(ctx context.Context, datasetID string) ([]model.CatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx, catalogSelect+` WHERE dataset_id = ? ORDER BY name`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CatalogEntry
	for rows.Next() {
		e, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// DatasetRegistrationCounts returns, for each dataset id, how many catalog
// entries reference it — used to render catalog status on the datasets list
// without an N+1 query.
func (s *Store) DatasetRegistrationCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT dataset_id, COUNT(*) FROM catalog_entries GROUP BY dataset_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// GetCatalogEntryByName returns a catalog entry by table name (nil if absent).
func (s *Store) GetCatalogEntryByName(ctx context.Context, name string) (*model.CatalogEntry, error) {
	return scanCatalogEntry(s.db.QueryRowContext(ctx, catalogSelect+` WHERE name = ?`, name))
}

// DeleteCatalogEntry removes a catalog entry by id.
func (s *Store) DeleteCatalogEntry(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM catalog_entries WHERE id = ?`, id)
	return err
}

func scanCatalogEntry(r rowScanner) (*model.CatalogEntry, error) {
	var e model.CatalogEntry
	var format, partitionCols, readOptions string
	var lastRefresh sql.NullTime
	if err := r.Scan(&e.ID, &e.Name, &e.DatasetID, &e.BucketName, &e.RootPath, &format,
		&e.URI, &e.Glob, &e.Enabled, &e.RefreshMode, &e.SchemaStrategy, &partitionCols, &readOptions,
		&e.Description, &e.CreatedAt, &e.UpdatedAt, &lastRefresh); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	e.Format = model.Format(format)
	e.LastRefreshAt = scanTime(lastRefresh)
	fromJSON(partitionCols, &e.PartitionCols)
	// Rows written before this column existed store "" — fall back to the safe
	// defaults; otherwise decode and normalize (empty Header -> "auto").
	if readOptions == "" {
		e.ReadOptions = model.DefaultReadOptions()
	} else {
		fromJSON(readOptions, &e.ReadOptions)
		e.ReadOptions = e.ReadOptions.Normalized()
	}
	return &e, nil
}
