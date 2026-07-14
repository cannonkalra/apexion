package storage

import (
	"context"
	"database/sql"

	"github.com/apexion/apexion/internal/model"
)

// UpsertBucket inserts or updates a bucket by id.
func (s *Store) UpsertBucket(ctx context.Context, b *model.Bucket) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO buckets (id, name, endpoint, region, object_count, total_size,
			dataset_count, schedule, created_at, updated_at, last_crawl_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, endpoint=excluded.endpoint, region=excluded.region,
			object_count=excluded.object_count, total_size=excluded.total_size,
			dataset_count=excluded.dataset_count, schedule=excluded.schedule,
			updated_at=excluded.updated_at, last_crawl_at=excluded.last_crawl_at`,
		b.ID, b.Name, b.Endpoint, b.Region, b.ObjectCount, b.TotalSize,
		b.DatasetCount, b.Schedule, b.CreatedAt, b.UpdatedAt, nullTime(b.LastCrawlAt))
	return err
}

// GetBucket returns a bucket by id.
func (s *Store) GetBucket(ctx context.Context, id string) (*model.Bucket, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, endpoint, region, object_count,
		total_size, dataset_count, schedule, created_at, updated_at, last_crawl_at
		FROM buckets WHERE id = ?`, id)
	return scanBucket(row)
}

// GetBucketByName returns a bucket by its name.
func (s *Store) GetBucketByName(ctx context.Context, name string) (*model.Bucket, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, endpoint, region, object_count,
		total_size, dataset_count, schedule, created_at, updated_at, last_crawl_at
		FROM buckets WHERE name = ?`, name)
	return scanBucket(row)
}

// ListBuckets returns all buckets ordered by name.
func (s *Store) ListBuckets(ctx context.Context) ([]model.Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, endpoint, region, object_count,
		total_size, dataset_count, schedule, created_at, updated_at, last_crawl_at
		FROM buckets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Bucket
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// RecomputeBucketStats refreshes denormalized counters from child tables.
func (s *Store) RecomputeBucketStats(ctx context.Context, bucketID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE buckets SET
			object_count = (SELECT COUNT(*) FROM objects WHERE bucket_id = ?),
			total_size   = COALESCE((SELECT SUM(size) FROM objects WHERE bucket_id = ?),0),
			dataset_count= (SELECT COUNT(*) FROM datasets WHERE bucket_id = ?)
		WHERE id = ?`, bucketID, bucketID, bucketID, bucketID)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBucket(r rowScanner) (*model.Bucket, error) {
	var b model.Bucket
	var last sql.NullTime
	if err := r.Scan(&b.ID, &b.Name, &b.Endpoint, &b.Region, &b.ObjectCount,
		&b.TotalSize, &b.DatasetCount, &b.Schedule, &b.CreatedAt, &b.UpdatedAt, &last); err != nil {
		return nil, err
	}
	b.LastCrawlAt = scanTime(last)
	return &b, nil
}
