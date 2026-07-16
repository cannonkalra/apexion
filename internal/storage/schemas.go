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
			if _, err := tx.ExecContext(ctx, statInsertSQL, statInsertArgs(c.ID, c.Statistics)...); err != nil {
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
	return scanStatistics(s.db.QueryRowContext(ctx,
		statSelect+` WHERE column_id = ?`, columnID))
}

const statColumns = `id, column_id, null_count, distinct_count, min_value, max_value,
	mean_value, sample_count, completeness, uniqueness, sample_values,
	std_dev, variance, q25, median, q75, min_length, max_length, avg_length,
	entropy, top_values, profiled_at`

const statSelect = `SELECT ` + statColumns + ` FROM statistics`

const statInsertSQL = `INSERT INTO statistics (` + statColumns + `)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// statInsertArgs builds the ordered argument list for statInsertSQL (matching
// statColumns). If the statistics carry no id, one is derived from the column id.
func statInsertArgs(columnID string, st *model.Statistics) []any {
	id := st.ID
	if id == "" {
		id = columnID + "-stat"
	}
	return []any{
		id, columnID, st.NullCount, st.DistinctCount, st.MinValue, st.MaxValue,
		meanArg(st.MeanValue), st.SampleCount, st.Completeness, st.Uniqueness,
		toJSON(st.SampleValues), meanArg(st.StdDev), meanArg(st.Variance),
		meanArg(st.Q25), meanArg(st.Median), meanArg(st.Q75),
		st.MinLength, st.MaxLength, st.AvgLength, st.Entropy,
		toJSON(st.TopValues), st.ProfiledAt,
	}
}

func meanArg(m *float64) any {
	if m == nil {
		return nil
	}
	return *m
}

func scanStatistics(r rowScanner) (*model.Statistics, error) {
	var st model.Statistics
	var mean, std, variance, q25, median, q75 sql.NullFloat64
	var samples, topValues string
	var profiledAt sql.NullTime
	err := r.Scan(&st.ID, &st.ColumnID, &st.NullCount, &st.DistinctCount, &st.MinValue,
		&st.MaxValue, &mean, &st.SampleCount, &st.Completeness, &st.Uniqueness, &samples,
		&std, &variance, &q25, &median, &q75, &st.MinLength, &st.MaxLength, &st.AvgLength,
		&st.Entropy, &topValues, &profiledAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	st.MeanValue = floatPtr(mean)
	st.StdDev = floatPtr(std)
	st.Variance = floatPtr(variance)
	st.Q25 = floatPtr(q25)
	st.Median = floatPtr(median)
	st.Q75 = floatPtr(q75)
	fromJSON(samples, &st.SampleValues)
	fromJSON(topValues, &st.TopValues)
	st.ProfiledAt = scanTime(profiledAt)
	return &st, nil
}

func floatPtr(n sql.NullFloat64) *float64 {
	if n.Valid {
		v := n.Float64
		return &v
	}
	return nil
}
