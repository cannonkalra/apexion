package storage

import (
	"context"
	"database/sql"

	"github.com/apexion/apexion/internal/model"
)

// SaveCrawlerRun inserts or updates a crawler run.
func (s *Store) SaveCrawlerRun(ctx context.Context, r *model.CrawlerRun) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO crawler_runs (id, bucket_id, bucket_name, mode, trigger, status,
			objects_scanned, objects_new, objects_changed, datasets_found, bytes_scanned,
			checkpoint, error, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			status=excluded.status, objects_scanned=excluded.objects_scanned,
			objects_new=excluded.objects_new, objects_changed=excluded.objects_changed,
			datasets_found=excluded.datasets_found, bytes_scanned=excluded.bytes_scanned,
			checkpoint=excluded.checkpoint, error=excluded.error, finished_at=excluded.finished_at`,
		r.ID, r.BucketID, r.BucketName, r.Mode, r.Trigger, r.Status, r.ObjectsScanned,
		r.ObjectsNew, r.ObjectsChanged, r.DatasetsFound, r.BytesScanned, r.Checkpoint,
		r.Error, r.StartedAt, nullTime(r.FinishedAt))
	return err
}

// GetCrawlerRun returns a run by id.
func (s *Store) GetCrawlerRun(ctx context.Context, id string) (*model.CrawlerRun, error) {
	row := s.db.QueryRowContext(ctx, crawlerRunSelect+` WHERE id = ?`, id)
	return scanCrawlerRun(row)
}

// ListCrawlerRuns lists recent crawler runs (optionally by bucket).

const crawlerRunSelect = `SELECT id, bucket_id, bucket_name, mode, trigger, status,
	objects_scanned, objects_new, objects_changed, datasets_found, bytes_scanned,
	checkpoint, error, started_at, finished_at FROM crawler_runs`

func scanCrawlerRun(r rowScanner) (*model.CrawlerRun, error) {
	var run model.CrawlerRun
	var fin sql.NullTime
	if err := r.Scan(&run.ID, &run.BucketID, &run.BucketName, &run.Mode, &run.Trigger,
		&run.Status, &run.ObjectsScanned, &run.ObjectsNew, &run.ObjectsChanged,
		&run.DatasetsFound, &run.BytesScanned, &run.Checkpoint, &run.Error,
		&run.StartedAt, &fin); err != nil {
		return nil, err
	}
	run.FinishedAt = scanTime(fin)
	return &run, nil
}
