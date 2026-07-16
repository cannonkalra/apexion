package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// UpsertDataset inserts or updates a dataset by id.
func (s *Store) UpsertDataset(ctx context.Context, d *model.Dataset) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO datasets (id, bucket_id, bucket_name, name, path, format, compression,
			file_count, total_size, row_count, partition_keys, schema_id, description,
			created_at, updated_at, last_scan_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			bucket_name=excluded.bucket_name, name=excluded.name, path=excluded.path,
			format=excluded.format, compression=excluded.compression,
			file_count=excluded.file_count, total_size=excluded.total_size,
			row_count=excluded.row_count, partition_keys=excluded.partition_keys,
			schema_id=excluded.schema_id, description=excluded.description,
			updated_at=excluded.updated_at, last_scan_at=excluded.last_scan_at`,
		d.ID, d.BucketID, d.BucketName, d.Name, d.Path, d.Format, d.Compression,
		d.FileCount, d.TotalSize, d.RowCount, toJSON(d.PartitionKeys), d.SchemaID,
		d.Description, d.CreatedAt, d.UpdatedAt, nullTime(d.LastScanAt))
	return err
}

// GetDataset returns a dataset by id.
func (s *Store) GetDataset(ctx context.Context, id string) (*model.Dataset, error) {
	row := s.db.QueryRowContext(ctx, datasetSelect+` WHERE id = ?`, id)
	return scanDataset(row)
}

// GetDatasetByPath finds a dataset by its bucket and common path.
func (s *Store) GetDatasetByPath(ctx context.Context, bucketID, path string) (*model.Dataset, error) {
	row := s.db.QueryRowContext(ctx, datasetSelect+` WHERE bucket_id = ? AND path = ?`, bucketID, path)
	return scanDataset(row)
}

// DatasetFilter narrows a dataset listing.
type DatasetFilter struct {
	BucketID string
	Format   string
	Search   string
	Limit    int
	Offset   int
}

// ListDatasets returns datasets matching the filter.
func (s *Store) ListDatasets(ctx context.Context, f DatasetFilter) ([]model.Dataset, error) {
	q := datasetSelect
	var where []string
	var args []any
	if f.BucketID != "" {
		where = append(where, "bucket_id = ?")
		args = append(args, f.BucketID)
	}
	if f.Format != "" {
		where = append(where, "format = ?")
		args = append(args, f.Format)
	}
	if f.Search != "" {
		where = append(where, "(LOWER(name) LIKE ? OR LOWER(path) LIKE ?)")
		like := "%" + strings.ToLower(f.Search) + "%"
		args = append(args, like, like)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Dataset
	for rows.Next() {
		d, err := scanDataset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDataset removes a dataset and its dependent schema/columns/partitions.
func (s *Store) DeleteDataset(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`DELETE FROM statistics WHERE column_id IN (SELECT c.id FROM columns c
			JOIN schemas s ON c.schema_id = s.id WHERE s.dataset_id = ?)`,
		`DELETE FROM columns WHERE schema_id IN (SELECT id FROM schemas WHERE dataset_id = ?)`,
		`DELETE FROM schemas WHERE dataset_id = ?`,
		`DELETE FROM partitions WHERE dataset_id = ?`,
		`DELETE FROM data_samples WHERE dataset_id = ?`,
		`DELETE FROM inference_runs WHERE dataset_id = ?`,
		`DELETE FROM dataset_profiles WHERE dataset_id = ?`,
		`UPDATE objects SET dataset_id = '' WHERE dataset_id = ?`,
		`DELETE FROM datasets WHERE id = ?`,
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const datasetSelect = `SELECT id, bucket_id, bucket_name, name, path, format, compression,
	file_count, total_size, row_count, partition_keys, schema_id, description,
	created_at, updated_at, last_scan_at FROM datasets`

func scanDataset(r rowScanner) (*model.Dataset, error) {
	var d model.Dataset
	var pk string
	var last sql.NullTime
	if err := r.Scan(&d.ID, &d.BucketID, &d.BucketName, &d.Name, &d.Path, &d.Format,
		&d.Compression, &d.FileCount, &d.TotalSize, &d.RowCount, &pk, &d.SchemaID,
		&d.Description, &d.CreatedAt, &d.UpdatedAt, &last); err != nil {
		return nil, err
	}
	fromJSON(pk, &d.PartitionKeys)
	d.LastScanAt = scanTime(last)
	return &d, nil
}
