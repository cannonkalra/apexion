package storage

import (
	"context"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// UpsertObject inserts or updates an object by id.
func (s *Store) UpsertObject(ctx context.Context, o *model.Object) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO objects (id, bucket_id, bucket_name, key, etag, version_id, size,
			format, compression, dataset_id, is_hidden, metadata_hash, storage_class,
			created_at, modified, discovered_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			etag=excluded.etag, version_id=excluded.version_id, size=excluded.size,
			format=excluded.format, compression=excluded.compression,
			dataset_id=excluded.dataset_id, is_hidden=excluded.is_hidden,
			metadata_hash=excluded.metadata_hash, storage_class=excluded.storage_class,
			modified=excluded.modified, discovered_at=excluded.discovered_at`,
		o.ID, o.BucketID, o.BucketName, o.Key, o.ETag, o.VersionID, o.Size,
		o.Format, o.Compression, o.DatasetID, o.IsHidden, o.MetadataHash,
		o.StorageClass, o.CreatedAt, o.Modified, o.DiscoveredAt)
	return err
}

// GetObjectHash returns the stored metadata hash for a key (for incremental
// crawls). The bool is false when the object is unknown.
func (s *Store) GetObjectHash(ctx context.Context, bucketID, key string) (string, string, bool, error) {
	var id, hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, metadata_hash FROM objects WHERE bucket_id = ? AND key = ?`,
		bucketID, key).Scan(&id, &hash)
	if err != nil {
		if isNoRows(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return id, hash, true, nil
}

// SetObjectDataset assigns an object to a dataset.
func (s *Store) SetObjectDataset(ctx context.Context, objectID, datasetID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE objects SET dataset_id = ? WHERE id = ?`, datasetID, objectID)
	return err
}

// AssignObjectsByPrefix links every object under a key prefix to a dataset. An
// empty prefix assigns all objects in the bucket.
func (s *Store) AssignObjectsByPrefix(ctx context.Context, bucketID, datasetID, prefix string) error {
	if prefix == "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE objects SET dataset_id = ? WHERE bucket_id = ?`, datasetID, bucketID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE objects SET dataset_id = ? WHERE bucket_id = ? AND key LIKE ?`,
		datasetID, bucketID, prefix+"/%")
	return err
}

// ListObjects lists objects for a bucket (optionally filtered by dataset).
func (s *Store) ListObjects(ctx context.Context, bucketID, datasetID string, limit int) ([]model.Object, error) {
	q := `SELECT id, bucket_id, bucket_name, key, etag, version_id, size, format,
		compression, dataset_id, is_hidden, metadata_hash, storage_class,
		created_at, modified, discovered_at FROM objects`
	var where []string
	var args []any
	if bucketID != "" {
		where = append(where, "bucket_id = ?")
		args = append(args, bucketID)
	}
	if datasetID != "" {
		where = append(where, "dataset_id = ?")
		args = append(args, datasetID)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY key"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Object
	for rows.Next() {
		var o model.Object
		if err := rows.Scan(&o.ID, &o.BucketID, &o.BucketName, &o.Key, &o.ETag,
			&o.VersionID, &o.Size, &o.Format, &o.Compression, &o.DatasetID, &o.IsHidden,
			&o.MetadataHash, &o.StorageClass, &o.CreatedAt, &o.Modified, &o.DiscoveredAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// SampleObjectKey returns one representative object key for a dataset, or "" if
// none. Callers use it to match a dataset's real file extension (including any
// compression suffix, e.g. .csv.gz) when building readers and views.
func (s *Store) SampleObjectKey(ctx context.Context, ds *model.Dataset) string {
	if ds == nil {
		return ""
	}
	objs, err := s.ListObjects(ctx, ds.BucketID, ds.ID, 1)
	if err != nil || len(objs) == 0 {
		return ""
	}
	return objs[0].Key
}

// CountObjects returns the total object count.
func (s *Store) CountObjects(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects`).Scan(&n)
	return n, err
}
