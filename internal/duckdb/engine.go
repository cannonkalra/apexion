// Package duckdb is the isolated DuckDB query engine. It runs read-only queries
// directly against objects in object storage (no download, no ETL) to power
// file preview, per-dataset preview, and directory-table creation. It uses its
// own in-memory DuckDB connection with the httpfs/s3 extension configured,
// separate from the catalog database in package storage.
//
// Files: engine.go (connection lifecycle, DuckDB S3 secret config, query
// execution) and tables.go (FROM-clause / directory-table expression builders).
// Explorer and catalog consume this engine through its exported methods.
//
// It deliberately does NOT: know about HTTP, the UI, or the API; own the catalog
// schema (that is package storage); or write to object storage (reads only).
package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
)

// Engine executes read-only DuckDB queries over S3 objects.
type Engine struct {
	db      *sql.DB
	log     zerolog.Logger
	ready   bool
	readErr string
}

// Result is a tabular query result.
type Result struct {
	Columns   []string   `json:"columns"`
	Types     []string   `json:"types"`
	Rows      [][]string `json:"rows"`
	RowCount  int        `json:"row_count"`
	Truncated bool       `json:"truncated"`
	SQL       string     `json:"sql"`
	Elapsed   string     `json:"elapsed"`
}

// New builds the preview engine and configures DuckDB's S3 access from the
// MinIO settings. The httpfs extension is installed/loaded on first use; if the
// environment is offline the engine still starts (previews will report the
// install error instead of crashing).
func New(cfg config.MinIOConfig, log zerolog.Logger) (*Engine, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	db.SetMaxOpenConns(1) // one shared in-memory instance
	e := &Engine{db: db, log: log.With().Str("component", "preview").Logger()}
	e.configure(cfg)
	return e, nil
}

func (e *Engine) configure(cfg config.MinIOConfig) {
	if !e.ensureExtensions() {
		return
	}
	_ = e.Reconfigure(objstore.PreviewConfig{
		Endpoint: cfg.Endpoint, Region: cfg.Region, AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey, UseSSL: cfg.UseSSL, URLStyle: "path",
	})
}

// ensureExtensions installs httpfs (required) and delta/iceberg (optional). It
// runs once; subsequent connection switches only re-apply credentials.
func (e *Engine) ensureExtensions() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, q := range []string{
		"SET autoinstall_known_extensions=true",
		"SET autoload_known_extensions=true",
		"INSTALL httpfs",
		"LOAD httpfs",
	} {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			e.readErr = err.Error()
			e.log.Warn().Err(err).Str("stmt", q).Msg("httpfs setup failed; S3 preview disabled")
			return false
		}
	}
	for _, ext := range []string{"delta", "iceberg"} {
		if _, err := e.db.ExecContext(ctx, "INSTALL "+ext); err != nil {
			e.log.Debug().Err(err).Str("ext", ext).Msg("optional extension install failed")
			continue
		}
		_, _ = e.db.ExecContext(ctx, "LOAD "+ext)
	}
	return true
}

// Reconfigure points the preview engine at a different connection. It is safe
// to call whenever the active connection changes.
func (e *Engine) Reconfigure(c objstore.PreviewConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	urlStyle := c.URLStyle
	if urlStyle == "" {
		urlStyle = "path"
	}
	scheme := "http://"
	if c.UseSSL {
		scheme = "https://"
	}

	useSSL := boolStr(c.UseSSL)

	// Delta/Iceberg kernels read AWS_* env vars rather than DuckDB's s3_*
	// settings. AWS_ALLOW_HTTP is required for plain-HTTP MinIO.
	env := map[string]string{
		"AWS_REGION":                 region,
		"AWS_DEFAULT_REGION":         region,
		"AWS_ALLOW_HTTP":             boolStr(!c.UseSSL),
		"AWS_S3_ALLOW_UNSAFE_RENAME": "true",
	}
	if c.Endpoint != "" {
		env["AWS_ENDPOINT_URL"] = scheme + c.Endpoint
		env["AWS_ENDPOINT_URL_S3"] = scheme + c.Endpoint
	}
	if c.UseRole {
		// Clear any static creds left in the process env by a previous
		// connection (e.g. the default MinIO one) so the credential chain
		// resolves the instance/service role via IMDS instead of stale keys.
		for _, k := range []string{
			"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
			"AWS_EC2_METADATA_DISABLED",
		} {
			_ = os.Unsetenv(k)
		}
	} else {
		env["AWS_ACCESS_KEY_ID"] = c.AccessKey
		env["AWS_SECRET_ACCESS_KEY"] = c.SecretKey
		env["AWS_EC2_METADATA_DISABLED"] = "true"
	}
	for k, v := range env {
		_ = os.Setenv(k, v)
	}

	setup := []string{
		"SET s3_use_ssl=" + useSSL,
		fmt.Sprintf("SET s3_url_style='%s'", esc(urlStyle)),
		fmt.Sprintf("SET s3_region='%s'", esc(region)),
	}
	if c.Endpoint != "" {
		setup = append(setup, fmt.Sprintf("SET s3_endpoint='%s'", esc(c.Endpoint)))
	}
	if !c.UseRole {
		setup = append(setup,
			fmt.Sprintf("SET s3_access_key_id='%s'", esc(c.AccessKey)),
			fmt.Sprintf("SET s3_secret_access_key='%s'", esc(c.SecretKey)))
	}
	for _, q := range setup {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			e.readErr = err.Error()
			e.log.Warn().Err(err).Msg("s3 config failed")
			return err
		}
	}

	// A DuckDB Secret is honored by httpfs, delta_scan, and iceberg_scan alike.
	endpointClause := ""
	if c.Endpoint != "" {
		endpointClause = fmt.Sprintf(", ENDPOINT '%s'", esc(c.Endpoint))
	}
	var secret string
	if c.UseRole {
		// PROVIDER credential_chain resolves creds from env/config/instance role.
		secret = fmt.Sprintf(`CREATE OR REPLACE SECRET apexion_s3 (
			TYPE S3, PROVIDER CREDENTIAL_CHAIN%s,
			URL_STYLE '%s', USE_SSL %s, REGION '%s')`,
			endpointClause, esc(urlStyle), useSSL, esc(region))
	} else {
		secret = fmt.Sprintf(`CREATE OR REPLACE SECRET apexion_s3 (
			TYPE S3, KEY_ID '%s', SECRET '%s'%s,
			URL_STYLE '%s', USE_SSL %s, REGION '%s')`,
			esc(c.AccessKey), esc(c.SecretKey), endpointClause, esc(urlStyle), useSSL, esc(region))
	}
	if _, err := e.db.ExecContext(ctx, secret); err != nil {
		e.log.Debug().Err(err).Msg("create s3 secret failed (table-format preview may be limited)")
	}

	e.ready = true
	e.readErr = ""
	e.log.Info().Str("endpoint", c.Endpoint).Str("url_style", urlStyle).Bool("role", c.UseRole).Msg("preview engine configured")
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Ready reports whether S3 preview is available.
func (e *Engine) Ready() bool { return e.ready }

// Close releases the DuckDB connection.
func (e *Engine) Close() error { return e.db.Close() }

// PreviewFile previews a single object with the safe defaults.
func (e *Engine) PreviewFile(ctx context.Context, bucket, key string, format model.Format, limit int) (*Result, error) {
	return e.PreviewFileWith(ctx, bucket, key, format, model.DefaultReadOptions(), limit)
}

// PreviewFileWith previews a single object with caller-supplied reader options
// (Header, UnionByName, IgnoreErrors, …) so the preview UI can regenerate live.
// The filename column is always suppressed — a spurious `filename` column on one
// file is noise. Result.SQL carries the exact executed SQL for display.
func (e *Engine) PreviewFileWith(ctx context.Context, bucket, key string, format model.Format, opts model.ReadOptions, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	opts.Filename = false
	from, err := FromClause(bucket, key, format, opts)
	if err != nil {
		return nil, err
	}
	return e.query(ctx, fmt.Sprintf("SELECT * FROM %s", from), limit)
}

// PreviewDataset previews a dataset by globbing its data files with the defaults.
func (e *Engine) PreviewDataset(ctx context.Context, bucket, prefix string, format model.Format, sampleKey string, limit int) (*Result, error) {
	return e.PreviewDatasetWith(ctx, bucket, prefix, format, sampleKey, model.DefaultReadOptions(), limit)
}

// PreviewDatasetWith previews a dataset with caller-supplied reader options.
func (e *Engine) PreviewDatasetWith(ctx context.Context, bucket, prefix string, format model.Format, sampleKey string, opts model.ReadOptions, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	opts.Filename = false
	from, err := globClause(bucket, prefix, format, sampleKey, opts)
	if err != nil {
		return nil, err
	}
	return e.query(ctx, fmt.Sprintf("SELECT * FROM %s", from), limit)
}

// PreviewDatasetPartitioned previews a dataset with its virtual partition columns
// included — the same columns the registered view exposes. For positional
// (bare-directory) datasets the columns are derived from the object path via
// split_part; with no partNames it falls back to the plain preview (which still
// surfaces native key=value hive partitions). This keeps the Preview tab
// consistent with the Partitions tab and the eventual SQL view.
func (e *Engine) PreviewDatasetPartitioned(ctx context.Context, bucket, prefix string, format model.Format, sampleKey string, partNames []string, opts model.ReadOptions, limit int) (*Result, error) {
	if len(partNames) == 0 {
		return e.PreviewDatasetWith(ctx, bucket, prefix, format, sampleKey, opts, limit)
	}
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	opts.Filename = false
	sel, err := partitionSelectSQL(bucket, prefix, format, sampleKey, partNames, opts)
	if err != nil {
		return nil, err
	}
	return e.query(ctx, sel, limit)
}

// query executes an arbitrary (already-built) query, applying a row limit.
func (e *Engine) query(ctx context.Context, q string, limit int) (*Result, error) {
	if limit <= 0 {
		limit = 100
	}
	// Apply an outer limit only to row-producing selects.
	wrapped := q
	upper := strings.ToUpper(strings.TrimSpace(q))
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		wrapped = fmt.Sprintf("SELECT * FROM (%s) AS _q LIMIT %d", q, limit+1)
	}

	start := time.Now()
	rows, err := e.db.QueryContext(ctx, wrapped)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	colTypes, _ := rows.ColumnTypes()
	types := make([]string, len(cols))
	for i, ct := range colTypes {
		types[i] = ct.DatabaseTypeName()
	}

	res := &Result{Columns: cols, Types: types, SQL: q}
	for rows.Next() {
		if len(res.Rows) >= limit {
			res.Truncated = true
			break
		}
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, stringifyRow(cells))
	}
	if err := rows.Err(); err != nil {
		return nil, cleanErr(err)
	}
	res.RowCount = len(res.Rows)
	res.Elapsed = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

func stringifyRow(cells []any) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = stringify(c)
	}
	return out
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ColumnDef is a column's name and its DuckDB type as reported by DESCRIBE.
type ColumnDef struct {
	Name string
	Type string
}

// DescribeReader returns the column names and types of a reader expression
// WITHOUT scanning the data: DESCRIBE reads only metadata — a Parquet footer, or
// the sample_size-bounded inference of read_csv_auto/read_json_auto. `from` is a
// reader expression such as read_parquet('s3://…') built by tables.go.
func (e *Engine) DescribeReader(ctx context.Context, from string) ([]ColumnDef, error) {
	if !e.ready {
		return nil, fmt.Errorf("schema discovery unavailable: %s", e.readErr)
	}
	rows, err := e.db.QueryContext(ctx, "DESCRIBE SELECT * FROM "+from)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer rows.Close()

	// DESCRIBE yields: column_name, column_type, null, key, default, extra.
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []ColumnDef
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, ColumnDef{Name: stringify(cells[0]), Type: stringify(cells[1])})
	}
	if err := rows.Err(); err != nil {
		return nil, cleanErr(err)
	}
	return out, nil
}

// DescribeFile returns the schema of a single object without scanning it. The
// filename column is suppressed (it is discovery noise, not part of the data
// schema). opts carry the reader options so CSV/JSON honour header/sample_size.
func (e *Engine) DescribeFile(ctx context.Context, bucket, key string, format model.Format, opts model.ReadOptions) ([]ColumnDef, error) {
	o := opts
	o.Filename = false
	from, err := FromClause(bucket, key, format, o)
	if err != nil {
		return nil, err
	}
	return e.DescribeReader(ctx, from)
}

// ParquetRowCount returns the total row count of a Parquet object from its
// footer metadata (parquet_metadata) — no row scan. Used only for all-Parquet
// selections; other formats have no cheap exact count.
func (e *Engine) ParquetRowCount(ctx context.Context, uri string) (int64, error) {
	if !e.ready {
		return 0, fmt.Errorf("row count unavailable: %s", e.readErr)
	}
	q := fmt.Sprintf("SELECT COALESCE(SUM(num_rows), 0) FROM parquet_file_metadata('%s')", esc(uri))
	var n int64
	if err := e.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, cleanErr(err)
	}
	return n, nil
}

func esc(s string) string { return strings.ReplaceAll(s, "'", "''") }

func cleanErr(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s", msg)
}
