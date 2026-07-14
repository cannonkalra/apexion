package storage

import (
	"context"

	"github.com/apexion/apexion/internal/model"
)

// ReplacePartitions removes existing partitions for a dataset and inserts the
// given set atomically.
func (s *Store) ReplacePartitions(ctx context.Context, datasetID string, parts []model.Partition) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM partitions WHERE dataset_id = ?`, datasetID); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO partitions (id, dataset_id, path, values, file_count, size, row_count, created_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			p.ID, datasetID, p.Path, toJSON(p.Values), p.FileCount, p.Size, p.RowCount, p.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListPartitions returns the partitions of a dataset.
func (s *Store) ListPartitions(ctx context.Context, datasetID string) ([]model.Partition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dataset_id, path, values, file_count, size, row_count, created_at
		 FROM partitions WHERE dataset_id = ? ORDER BY path`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Partition
	for rows.Next() {
		var p model.Partition
		var vals string
		if err := rows.Scan(&p.ID, &p.DatasetID, &p.Path, &vals, &p.FileCount,
			&p.Size, &p.RowCount, &p.CreatedAt); err != nil {
			return nil, err
		}
		fromJSON(vals, &p.Values)
		out = append(out, p)
	}
	return out, rows.Err()
}
