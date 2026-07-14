package storage

import (
	"context"
	"database/sql"

	"github.com/apexion/apexion/internal/model"
)

// SaveJob inserts or updates a job.
func (s *Store) SaveJob(ctx context.Context, j *model.Job) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, status, ref_id, label, progress, message, error,
			created_at, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			status=excluded.status, progress=excluded.progress, message=excluded.message,
			error=excluded.error, started_at=excluded.started_at, finished_at=excluded.finished_at`,
		j.ID, j.Type, j.Status, j.RefID, j.Label, j.Progress, j.Message, j.Error,
		j.CreatedAt, nullTime(j.StartedAt), nullTime(j.FinishedAt))
	return err
}

// GetJob returns a job by id.
func (s *Store) GetJob(ctx context.Context, id string) (*model.Job, error) {
	row := s.db.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id)
	return scanJob(row)
}

// ListJobs lists recent jobs (optionally filtered by status).
func (s *Store) ListJobs(ctx context.Context, status string, limit int) ([]model.Job, error) {
	q := jobSelect
	var args []any
	if status != "" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// CountRunningJobs returns the number of queued or running jobs.
func (s *Store) CountRunningJobs(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running')`).Scan(&n)
	return n, err
}

const jobSelect = `SELECT id, type, status, ref_id, label, progress, message, error,
	created_at, started_at, finished_at FROM jobs`

func scanJob(r rowScanner) (*model.Job, error) {
	var j model.Job
	var start, fin sql.NullTime
	if err := r.Scan(&j.ID, &j.Type, &j.Status, &j.RefID, &j.Label, &j.Progress,
		&j.Message, &j.Error, &j.CreatedAt, &start, &fin); err != nil {
		return nil, err
	}
	j.StartedAt = scanTime(start)
	j.FinishedAt = scanTime(fin)
	return &j, nil
}
