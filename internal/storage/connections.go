package storage

import (
	"context"
	"time"

	"github.com/apexion/apexion/internal/model"
)

// UpsertConnection inserts or updates a connection profile.
func (s *Store) UpsertConnection(ctx context.Context, c *model.Connection) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO connections (id, name, provider, endpoint, region, access_key,
			secret_key, session_token, use_ssl, use_role, path_style, is_active, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, provider=excluded.provider, endpoint=excluded.endpoint,
			region=excluded.region, access_key=excluded.access_key,
			secret_key=excluded.secret_key, session_token=excluded.session_token,
			use_ssl=excluded.use_ssl,
			use_role=excluded.use_role, path_style=excluded.path_style,
			updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Provider, c.Endpoint, c.Region, c.AccessKey, c.SecretKey,
		c.SessionToken, c.UseSSL, c.UseRole, c.PathStyle, c.IsActive, c.CreatedAt, c.UpdatedAt)
	return err
}

// ListConnections returns all connection profiles ordered by name.
func (s *Store) ListConnections(ctx context.Context) ([]model.Connection, error) {
	rows, err := s.db.QueryContext(ctx, connectionSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetConnection returns a connection by id.
func (s *Store) GetConnection(ctx context.Context, id string) (*model.Connection, error) {
	row := s.db.QueryRowContext(ctx, connectionSelect+` WHERE id = ?`, id)
	c, err := scanConnection(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// GetActiveConnection returns the active connection, or nil if none is set.
func (s *Store) GetActiveConnection(ctx context.Context) (*model.Connection, error) {
	row := s.db.QueryRowContext(ctx, connectionSelect+` WHERE is_active = TRUE LIMIT 1`)
	c, err := scanConnection(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// CountConnections returns the number of stored connections.
func (s *Store) CountConnections(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connections`).Scan(&n)
	return n, err
}

// SetActiveConnection makes one connection active and all others inactive.
func (s *Store) SetActiveConnection(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE connections SET is_active = FALSE`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE connections SET is_active = TRUE, updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteConnection removes a connection. The caller must ensure another
// connection is activated if the deleted one was active.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, id)
	return err
}

const connectionSelect = `SELECT id, name, provider, endpoint, region, access_key,
	secret_key, session_token, use_ssl, use_role, path_style, is_active, created_at, updated_at FROM connections`

func scanConnection(r rowScanner) (*model.Connection, error) {
	var c model.Connection
	if err := r.Scan(&c.ID, &c.Name, &c.Provider, &c.Endpoint, &c.Region,
		&c.AccessKey, &c.SecretKey, &c.SessionToken, &c.UseSSL, &c.UseRole, &c.PathStyle,
		&c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}
