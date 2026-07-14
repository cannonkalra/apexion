package storage

import (
	"context"

	"github.com/apexion/apexion/internal/model"
)

// SaveEvent persists a domain event.
func (s *Store) SaveEvent(ctx context.Context, e *model.Event) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (id, type, subject, payload, created_at) VALUES (?,?,?,?,?)`,
		e.ID, e.Type, e.Subject, e.Payload, e.CreatedAt)
	return err
}

// ListEvents lists recent events (optionally by type).
func (s *Store) ListEvents(ctx context.Context, eventType string, limit int) ([]model.Event, error) {
	q := `SELECT id, type, subject, payload, created_at FROM events`
	var args []any
	if eventType != "" {
		q += " WHERE type = ?"
		args = append(args, eventType)
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
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.Type, &e.Subject, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveSample stores a data sample for a dataset (replacing any prior sample).
func (s *Store) SaveSample(ctx context.Context, sm *model.DataSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM data_samples WHERE dataset_id = ?`, sm.DatasetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO data_samples (id, dataset_id, rows_json, row_count, created_at)
		 VALUES (?,?,?,?,?)`,
		sm.ID, sm.DatasetID, sm.RowsJSON, sm.RowCount, sm.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// GetSample returns the stored sample for a dataset (nil if none).
func (s *Store) GetSample(ctx context.Context, datasetID string) (*model.DataSample, error) {
	var sm model.DataSample
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dataset_id, rows_json, row_count, created_at FROM data_samples
		 WHERE dataset_id = ? LIMIT 1`, datasetID).
		Scan(&sm.ID, &sm.DatasetID, &sm.RowsJSON, &sm.RowCount, &sm.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sm, nil
}
