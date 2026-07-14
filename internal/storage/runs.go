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
func (s *Store) ListCrawlerRuns(ctx context.Context, bucketID string, limit int) ([]model.CrawlerRun, error) {
	q := crawlerRunSelect
	var args []any
	if bucketID != "" {
		q += " WHERE bucket_id = ?"
		args = append(args, bucketID)
	}
	q += " ORDER BY started_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CrawlerRun
	for rows.Next() {
		r, err := scanCrawlerRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

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

// SaveInferenceRun inserts or updates an inference run.
func (s *Store) SaveInferenceRun(ctx context.Context, r *model.InferenceRun) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO inference_runs (id, dataset_id, dataset_name, status, sample_rows,
			quality_score, findings, error, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			status=excluded.status, sample_rows=excluded.sample_rows,
			quality_score=excluded.quality_score, findings=excluded.findings,
			error=excluded.error, finished_at=excluded.finished_at`,
		r.ID, r.DatasetID, r.DatasetName, r.Status, r.SampleRows, r.QualityScore,
		r.Findings, r.Error, r.StartedAt, nullTime(r.FinishedAt))
	return err
}

// GetInferenceRun returns an inference run by id.
func (s *Store) GetInferenceRun(ctx context.Context, id string) (*model.InferenceRun, error) {
	row := s.db.QueryRowContext(ctx, inferenceRunSelect+` WHERE id = ?`, id)
	return scanInferenceRun(row)
}

// LatestInferenceRun returns the most recent completed run for a dataset.
func (s *Store) LatestInferenceRun(ctx context.Context, datasetID string) (*model.InferenceRun, error) {
	row := s.db.QueryRowContext(ctx, inferenceRunSelect+
		` WHERE dataset_id = ? ORDER BY started_at DESC LIMIT 1`, datasetID)
	r, err := scanInferenceRun(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// ListInferenceRuns lists recent inference runs.
func (s *Store) ListInferenceRuns(ctx context.Context, datasetID string, limit int) ([]model.InferenceRun, error) {
	q := inferenceRunSelect
	var args []any
	if datasetID != "" {
		q += " WHERE dataset_id = ?"
		args = append(args, datasetID)
	}
	q += " ORDER BY started_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.InferenceRun
	for rows.Next() {
		r, err := scanInferenceRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

const inferenceRunSelect = `SELECT id, dataset_id, dataset_name, status, sample_rows,
	quality_score, findings, error, started_at, finished_at FROM inference_runs`

func scanInferenceRun(r rowScanner) (*model.InferenceRun, error) {
	var run model.InferenceRun
	var fin sql.NullTime
	if err := r.Scan(&run.ID, &run.DatasetID, &run.DatasetName, &run.Status,
		&run.SampleRows, &run.QualityScore, &run.Findings, &run.Error,
		&run.StartedAt, &fin); err != nil {
		return nil, err
	}
	run.FinishedAt = scanTime(fin)
	return &run, nil
}
