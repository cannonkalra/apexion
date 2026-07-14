package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/apexion/apexion/internal/model"
)

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// SaveSchema persists a schema together with its columns and statistics in one
// transaction. It returns the assigned version.
func (s *Store) SaveSchema(ctx context.Context, sc *model.Schema) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schemas (id, dataset_id, version, fingerprint, created_at)
		 VALUES (?,?,?,?,?)`,
		sc.ID, sc.DatasetID, sc.Version, sc.Fingerprint, sc.CreatedAt); err != nil {
		return err
	}
	for _, c := range sc.Columns {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO columns (id, schema_id, name, position, data_type, physical_type,
				nullable, semantic_type, is_partition, description)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			c.ID, sc.ID, c.Name, c.Position, c.DataType, c.PhysicalType,
			c.Nullable, c.SemanticType, c.IsPartition, c.Description); err != nil {
			return err
		}
		if c.Statistics != nil {
			st := c.Statistics
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO statistics (id, column_id, null_count, distinct_count, min_value,
					max_value, mean_value, sample_count, completeness, uniqueness, sample_values)
				 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				st.ID, c.ID, st.NullCount, st.DistinctCount, st.MinValue, st.MaxValue,
				meanArg(st.MeanValue), st.SampleCount, st.Completeness, st.Uniqueness,
				toJSON(st.SampleValues)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// LatestSchema returns the highest-version schema for a dataset (with columns).
func (s *Store) LatestSchema(ctx context.Context, datasetID string) (*model.Schema, error) {
	var sc model.Schema
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dataset_id, version, fingerprint, created_at FROM schemas
		 WHERE dataset_id = ? ORDER BY version DESC LIMIT 1`, datasetID).
		Scan(&sc.ID, &sc.DatasetID, &sc.Version, &sc.Fingerprint, &sc.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	cols, err := s.GetColumns(ctx, sc.ID)
	if err != nil {
		return nil, err
	}
	sc.Columns = cols
	return &sc, nil
}

// GetSchema returns a schema by id including columns.
func (s *Store) GetSchema(ctx context.Context, id string) (*model.Schema, error) {
	var sc model.Schema
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dataset_id, version, fingerprint, created_at FROM schemas WHERE id = ?`, id).
		Scan(&sc.ID, &sc.DatasetID, &sc.Version, &sc.Fingerprint, &sc.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	cols, err := s.GetColumns(ctx, sc.ID)
	if err != nil {
		return nil, err
	}
	sc.Columns = cols
	return &sc, nil
}

// LatestSchemaVersion returns the current max version for a dataset (0 if none).
func (s *Store) LatestSchemaVersion(ctx context.Context, datasetID string) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM schemas WHERE dataset_id = ?`, datasetID).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// GetColumns returns the columns of a schema with statistics attached.
func (s *Store) GetColumns(ctx context.Context, schemaID string) ([]model.Column, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, schema_id, name, position, data_type, physical_type, nullable,
			semantic_type, is_partition, description FROM columns
		 WHERE schema_id = ? ORDER BY position`, schemaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Column
	for rows.Next() {
		var c model.Column
		if err := rows.Scan(&c.ID, &c.SchemaID, &c.Name, &c.Position, &c.DataType,
			&c.PhysicalType, &c.Nullable, &c.SemanticType, &c.IsPartition, &c.Description); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range cols {
		st, err := s.getStatistics(ctx, cols[i].ID)
		if err != nil {
			return nil, err
		}
		cols[i].Statistics = st
	}
	return cols, nil
}

func (s *Store) getStatistics(ctx context.Context, columnID string) (*model.Statistics, error) {
	var st model.Statistics
	var mean sql.NullFloat64
	var samples string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, column_id, null_count, distinct_count, min_value, max_value,
			mean_value, sample_count, completeness, uniqueness, sample_values
		 FROM statistics WHERE column_id = ?`, columnID).
		Scan(&st.ID, &st.ColumnID, &st.NullCount, &st.DistinctCount, &st.MinValue,
			&st.MaxValue, &mean, &st.SampleCount, &st.Completeness, &st.Uniqueness, &samples)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	if mean.Valid {
		m := mean.Float64
		st.MeanValue = &m
	}
	fromJSON(samples, &st.SampleValues)
	return &st, nil
}

// UpdateColumnSemantic sets the semantic type of a column (by schema + name),
// used by the inference engine to enrich the stored schema.
func (s *Store) UpdateColumnSemantic(ctx context.Context, schemaID, name, semantic string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE columns SET semantic_type = ? WHERE schema_id = ? AND name = ?`,
		semantic, schemaID, name)
	return err
}

// SearchColumns finds columns whose name matches a query (global search).
func (s *Store) SearchColumns(ctx context.Context, query string, limit int) ([]ColumnHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.name, c.data_type, c.semantic_type, d.id, d.name
		FROM columns c
		JOIN schemas s ON c.schema_id = s.id
		JOIN datasets d ON s.dataset_id = d.id
		WHERE LOWER(c.name) LIKE ?
		ORDER BY c.name LIMIT ?`, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnHit
	for rows.Next() {
		var h ColumnHit
		if err := rows.Scan(&h.Column, &h.DataType, &h.SemanticType, &h.DatasetID, &h.DatasetName); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ColumnHit is a search result for a column.
type ColumnHit struct {
	Column       string `json:"column"`
	DataType     string `json:"data_type"`
	SemanticType string `json:"semantic_type"`
	DatasetID    string `json:"dataset_id"`
	DatasetName  string `json:"dataset_name"`
}

func meanArg(m *float64) any {
	if m == nil {
		return nil
	}
	return *m
}
