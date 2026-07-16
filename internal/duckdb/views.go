package duckdb

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidIdentifier reports whether name is a safe SQL identifier (letters, digits,
// underscore; not starting with a digit). Catalog table names must satisfy it.
func ValidIdentifier(name string) bool { return identRe.MatchString(name) }

// CreateView registers a catalog table as a DuckDB view that reads a dataset's
// files directly from object storage. No data is copied or ingested; directory
// partition keys (e.g. year=2026/month=07) surface as columns via
// hive_partitioning. Calling it again with the same name refreshes the view.
func (e *Engine) CreateView(ctx context.Context, name, bucket, prefix string, format model.Format, sampleKey string, opts model.ReadOptions) error {
	if !ValidIdentifier(name) {
		return fmt.Errorf("invalid table name %q", name)
	}
	if !e.ready {
		return fmt.Errorf("query engine unavailable: %s", e.readErr)
	}
	from, err := globClause(bucket, prefix, format, sampleKey, opts)
	if err != nil {
		return err
	}
	stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW "%s" AS SELECT * FROM %s`, name, from)
	if _, err := e.db.ExecContext(ctx, stmt); err != nil {
		return cleanErr(err)
	}
	e.log.Info().Str("table", name).Msg("catalog view created")
	return nil
}

// CreateViewPartitioned registers a view that additionally exposes bare-directory
// levels as virtual partition columns (partNames, in order). DuckDB cannot
// hive-infer non-key=value paths, so the columns are derived from the source
// path with split_part — plain string splitting, not regex — reading the
// original objects directly (no copy). With no partNames it is exactly
// CreateView.
func (e *Engine) CreateViewPartitioned(ctx context.Context, name, bucket, prefix string, format model.Format, sampleKey string, partNames []string, opts model.ReadOptions) error {
	if len(partNames) == 0 {
		return e.CreateView(ctx, name, bucket, prefix, format, sampleKey, opts)
	}
	if !ValidIdentifier(name) {
		return fmt.Errorf("invalid table name %q", name)
	}
	if !e.ready {
		return fmt.Errorf("query engine unavailable: %s", e.readErr)
	}
	stmt, err := partitionViewSQL(name, bucket, prefix, format, sampleKey, partNames, opts)
	if err != nil {
		return err
	}
	if _, err := e.db.ExecContext(ctx, stmt); err != nil {
		return cleanErr(err)
	}
	e.log.Info().Str("table", name).Int("partitions", len(partNames)).Msg("catalog view created (virtual partitions)")
	return nil
}

// partitionViewSQL builds the CREATE VIEW statement that derives virtual
// partition columns from the object path with split_part (string splitting, not
// regex). It is pure so the SQL can be unit-tested without a live engine.
func partitionViewSQL(name, bucket, prefix string, format model.Format, sampleKey string, partNames []string, opts model.ReadOptions) (string, error) {
	reader, err := partitionGlobReader(bucket, prefix, format, sampleKey, opts)
	if err != nil {
		return "", err
	}
	// Byte length of the constant path prefix that precedes the partition dirs,
	// e.g. "s3://bucket/root/". DuckDB substr/split_part are 1-indexed.
	prefixLen := len(fmt.Sprintf("s3://%s/%s/", bucket, strings.Trim(prefix, "/")))
	cols := make([]string, 0, len(partNames))
	for i, pn := range partNames {
		if !ValidIdentifier(pn) {
			return "", fmt.Errorf("invalid partition column %q", pn)
		}
		cols = append(cols, fmt.Sprintf(`split_part(substr(filename, %d), '/', %d) AS "%s"`, prefixLen+1, i+1, pn))
	}
	// The reader always exposes `filename` (partitionGlobReader forces it) so the
	// derived columns can split it. If the user asked to see the filename column,
	// keep it in the output; otherwise drop it with EXCLUDE.
	projection := "* EXCLUDE (filename)"
	if opts.Filename {
		projection = "*"
	}
	return fmt.Sprintf(`CREATE OR REPLACE VIEW "%s" AS SELECT %s, %s FROM %s`,
		name, projection, strings.Join(cols, ", "), reader), nil
}

// DropView removes a catalog table's view.
func (e *Engine) DropView(ctx context.Context, name string) error {
	if !ValidIdentifier(name) {
		return fmt.Errorf("invalid table name %q", name)
	}
	_, err := e.db.ExecContext(ctx, fmt.Sprintf(`DROP VIEW IF EXISTS "%s"`, name))
	return err
}

// Query runs a read-only SQL statement against the registered catalog views and
// returns up to `limit` rows. Only read statements are permitted.
func (e *Engine) Query(ctx context.Context, sql string, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("query engine unavailable: %s", e.readErr)
	}
	if err := readOnly(sql); err != nil {
		return nil, err
	}
	return e.query(ctx, sql, limit)
}

// readOnly rejects statements that are not read-only (no DDL/DML), so the query
// page cannot mutate the in-memory catalog or issue writes. It is a defence in
// depth: the query still runs via database/sql QueryContext, but we reject
// obviously-writing or multi-statement input up front rather than relying on the
// driver alone.
func readOnly(sql string) error {
	// Reject multi-statement input (e.g. "SELECT 1; DROP TABLE x"): allow only a
	// single optional trailing semicolon.
	if trimmed := strings.TrimRight(strings.TrimSpace(sql), ";"); strings.Contains(trimmed, ";") {
		return fmt.Errorf("only a single statement is allowed")
	}
	s := strings.TrimSpace(strings.ToUpper(sql))
	for len(s) > 0 && (strings.HasPrefix(s, "(") || strings.HasPrefix(s, "--")) {
		if strings.HasPrefix(s, "--") {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return fmt.Errorf("empty query")
		}
		s = strings.TrimSpace(s[1:])
	}
	switch {
	case strings.HasPrefix(s, "SELECT"), strings.HasPrefix(s, "WITH"),
		strings.HasPrefix(s, "DESCRIBE"), strings.HasPrefix(s, "SUMMARIZE"),
		strings.HasPrefix(s, "EXPLAIN"), strings.HasPrefix(s, "SHOW"),
		strings.HasPrefix(s, "PRAGMA"), strings.HasPrefix(s, "VALUES"),
		strings.HasPrefix(s, "TABLE"), strings.HasPrefix(s, "FROM"):
		return nil
	default:
		return fmt.Errorf("only read-only queries are allowed")
	}
}
