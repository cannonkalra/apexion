// Package storage is the persistence layer. It embeds DuckDB, runs migrations,
// and exposes repository methods over the normalized catalog schema.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/migrations"
)

// Store owns the DuckDB handle and all repository methods (defined across the
// files in this package). It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	log zerolog.Logger
}

// Open opens (or creates) the DuckDB database at path and configures the pool.
func Open(path string, maxOpenConns int, log zerolog.Logger) (*Store, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	if maxOpenConns <= 0 {
		maxOpenConns = 1
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping duckdb: %w", err)
	}
	return &Store{db: db, log: log.With().Str("component", "storage").Logger()}, nil
}

// DB exposes the raw handle for advanced queries (search, aggregates).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies every embedded migration that has not yet been applied.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		if applied[name] {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w\nstatement: %s", name, err, stmt)
			}
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, time.Now().UTC()); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
		s.log.Info().Str("migration", name).Msg("applied migration")
	}
	return nil
}

// splitStatements splits a SQL file into individual statements on ';'
// boundaries. The migration SQL contains no semicolons inside literals.
func splitStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// Drop pure-comment / whitespace chunks.
		trimmed := stripSQLComments(p)
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// JSON helpers for array/map columns.
// ---------------------------------------------------------------------------

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func fromJSON[T any](s string, dst *T) {
	if s == "" {
		return
	}
	_ = json.Unmarshal([]byte(s), dst)
}

// nullTime converts a *time.Time to a driver-friendly value.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// scanTime reads a sql.NullTime into a *time.Time.
func scanTime(nt sql.NullTime) *time.Time {
	if nt.Valid {
		u := nt.Time
		return &u
	}
	return nil
}
