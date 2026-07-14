package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/apexion/apexion/internal/model"
)

// DashboardStats aggregates headline metrics for the dashboard cards.
type DashboardStats struct {
	Buckets     int64      `json:"buckets"`
	Datasets    int64      `json:"datasets"`
	Objects     int64      `json:"objects"`
	Tables      int64      `json:"tables"` // schemas
	Columns     int64      `json:"columns"`
	RunningJobs int64      `json:"running_jobs"`
	TotalSize   int64      `json:"total_size"`
	LastCrawlAt *time.Time `json:"last_crawl_at"`
}

// Dashboard returns aggregate counts for the dashboard.
func (s *Store) Dashboard(ctx context.Context) (*DashboardStats, error) {
	var d DashboardStats
	scalar := func(q string) (int64, error) {
		var n sql.NullInt64
		err := s.db.QueryRowContext(ctx, q).Scan(&n)
		return n.Int64, err
	}
	var err error
	if d.Buckets, err = scalar(`SELECT COUNT(*) FROM buckets`); err != nil {
		return nil, err
	}
	if d.Datasets, err = scalar(`SELECT COUNT(*) FROM datasets`); err != nil {
		return nil, err
	}
	if d.Objects, err = scalar(`SELECT COUNT(*) FROM objects`); err != nil {
		return nil, err
	}
	if d.Tables, err = scalar(`SELECT COUNT(DISTINCT dataset_id) FROM schemas`); err != nil {
		return nil, err
	}
	if d.Columns, err = scalar(`SELECT COUNT(*) FROM columns`); err != nil {
		return nil, err
	}
	if d.RunningJobs, err = scalar(`SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running')`); err != nil {
		return nil, err
	}
	if d.TotalSize, err = scalar(`SELECT COALESCE(SUM(size),0) FROM objects`); err != nil {
		return nil, err
	}
	var last sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(started_at) FROM crawler_runs`).Scan(&last); err != nil {
		return nil, err
	}
	d.LastCrawlAt = scanTime(last)
	return &d, nil
}

// FormatBreakdown returns dataset counts grouped by format (for charts).
func (s *Store) FormatBreakdown(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT format, COUNT(*) FROM datasets GROUP BY format`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var f string
		var n int64
		if err := rows.Scan(&f, &n); err != nil {
			return nil, err
		}
		out[f] = n
	}
	return out, rows.Err()
}

// SearchResults bundles cross-entity search hits.
type SearchResults struct {
	Datasets []model.Dataset `json:"datasets"`
	Buckets  []model.Bucket  `json:"buckets"`
	Columns  []ColumnHit     `json:"columns"`
}

// Search runs a global search across datasets, buckets, and columns.
func (s *Store) Search(ctx context.Context, query string, limit int) (*SearchResults, error) {
	if limit <= 0 {
		limit = 20
	}
	ds, err := s.ListDatasets(ctx, DatasetFilter{Search: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	cols, err := s.SearchColumns(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	brows, err := s.db.QueryContext(ctx,
		`SELECT id, name, endpoint, region, object_count, total_size, dataset_count,
			schedule, created_at, updated_at, last_crawl_at FROM buckets
		 WHERE LOWER(name) LIKE ? LIMIT ?`, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer brows.Close()
	var buckets []model.Bucket
	for brows.Next() {
		b, err := scanBucket(brows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, *b)
	}
	return &SearchResults{Datasets: ds, Buckets: buckets, Columns: cols}, nil
}
