package storage

import (
	"context"
	"database/sql"

	"github.com/apexion/apexion/internal/model"
)

const catalogSelect = `SELECT id, name, dataset_id, bucket_name, root_path, format,
	uri, glob_pattern, enabled, refresh_mode, schema_strategy, partition_cols, description,
	created_at, updated_at, last_refresh_at FROM catalog_entries`

// UpsertCatalogEntry inserts or updates a catalog entry (by id).
func (s *Store) UpsertCatalogEntry(ctx context.Context, e *model.CatalogEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO catalog_entries (id, name, dataset_id, bucket_name, root_path, format,
			uri, glob_pattern, enabled, refresh_mode, schema_strategy, partition_cols, description,
			created_at, updated_at, last_refresh_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, bucket_name=excluded.bucket_name, root_path=excluded.root_path,
			format=excluded.format, uri=excluded.uri, glob_pattern=excluded.glob_pattern, enabled=excluded.enabled,
			refresh_mode=excluded.refresh_mode, schema_strategy=excluded.schema_strategy,
			partition_cols=excluded.partition_cols, description=excluded.description,
			updated_at=excluded.updated_at, last_refresh_at=excluded.last_refresh_at`,
		e.ID, e.Name, e.DatasetID, e.BucketName, e.RootPath, string(e.Format),
		e.URI, e.Glob, e.Enabled, e.RefreshMode, e.SchemaStrategy, toJSON(e.PartitionCols),
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
	var format, partitionCols string
	var lastRefresh sql.NullTime
	if err := r.Scan(&e.ID, &e.Name, &e.DatasetID, &e.BucketName, &e.RootPath, &format,
		&e.URI, &e.Glob, &e.Enabled, &e.RefreshMode, &e.SchemaStrategy, &partitionCols,
		&e.Description, &e.CreatedAt, &e.UpdatedAt, &lastRefresh); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	e.Format = model.Format(format)
	e.LastRefreshAt = scanTime(lastRefresh)
	fromJSON(partitionCols, &e.PartitionCols)
	return &e, nil
}
