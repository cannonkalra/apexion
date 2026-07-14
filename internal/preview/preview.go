// Package preview runs DuckDB queries directly against objects in MinIO/S3 —
// no download, no ETL. It powers the instant file preview, per-dataset preview,
// and the SQL scratchpad. It uses its own in-memory DuckDB connection with the
// httpfs/s3 extension configured, isolated from the catalog database.
package preview

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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The Delta/Iceberg extensions use their own object store (delta-kernel-rs /
	// pyiceberg-style) that reads AWS_* env vars rather than DuckDB's s3_*
	// settings, so we export both. AWS_ALLOW_HTTP is required for plain-HTTP
	// MinIO.
	scheme := "http://"
	if cfg.UseSSL {
		scheme = "https://"
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	setenv := map[string]string{
		"AWS_ENDPOINT_URL":           scheme + cfg.Endpoint,
		"AWS_ENDPOINT_URL_S3":        scheme + cfg.Endpoint,
		"AWS_ACCESS_KEY_ID":          cfg.AccessKey,
		"AWS_SECRET_ACCESS_KEY":      cfg.SecretKey,
		"AWS_REGION":                 region,
		"AWS_DEFAULT_REGION":         region,
		"AWS_ALLOW_HTTP":             "true",
		"AWS_S3_ALLOW_UNSAFE_RENAME": "true",
		"AWS_EC2_METADATA_DISABLED":  "true", // stop the 169.254.169.254 lookups
	}
	for k, v := range setenv {
		_ = os.Setenv(k, v)
	}

	// Best-effort extension install (needs network the first time). httpfs is
	// required; delta/iceberg are optional (table-format preview).
	for _, q := range []string{
		"SET autoinstall_known_extensions=true",
		"SET autoload_known_extensions=true",
		"INSTALL httpfs",
		"LOAD httpfs",
	} {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			e.readErr = err.Error()
			e.log.Warn().Err(err).Str("stmt", q).Msg("httpfs setup failed; S3 preview disabled")
			return
		}
	}
	for _, ext := range []string{"delta", "iceberg"} {
		if _, err := e.db.ExecContext(ctx, "INSTALL "+ext); err != nil {
			e.log.Debug().Err(err).Str("ext", ext).Msg("optional extension install failed")
			continue
		}
		_, _ = e.db.ExecContext(ctx, "LOAD "+ext)
	}

	useSSL := "false"
	if cfg.UseSSL {
		useSSL = "true"
	}
	setup := []string{
		fmt.Sprintf("SET s3_endpoint='%s'", esc(cfg.Endpoint)),
		"SET s3_use_ssl=" + useSSL,
		"SET s3_url_style='path'",
		fmt.Sprintf("SET s3_region='%s'", esc(region)),
		fmt.Sprintf("SET s3_access_key_id='%s'", esc(cfg.AccessKey)),
		fmt.Sprintf("SET s3_secret_access_key='%s'", esc(cfg.SecretKey)),
	}
	for _, q := range setup {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			e.readErr = err.Error()
			e.log.Warn().Err(err).Msg("s3 config failed")
			return
		}
	}

	// A DuckDB Secret is the modern, unified S3 credential mechanism honored by
	// httpfs, delta_scan, and iceberg_scan alike (unlike the legacy SET s3_*
	// settings, which the table-format kernels ignore).
	secret := fmt.Sprintf(`CREATE OR REPLACE SECRET apexion_s3 (
		TYPE S3, KEY_ID '%s', SECRET '%s', ENDPOINT '%s',
		URL_STYLE 'path', USE_SSL %s, REGION '%s')`,
		esc(cfg.AccessKey), esc(cfg.SecretKey), esc(cfg.Endpoint), useSSL, esc(region))
	if _, err := e.db.ExecContext(ctx, secret); err != nil {
		e.log.Debug().Err(err).Msg("create s3 secret failed (table-format preview may be limited)")
	}

	e.ready = true
	e.log.Info().Str("endpoint", cfg.Endpoint).Msg("preview engine ready (DuckDB httpfs/s3)")
}

// Ready reports whether S3 preview is available.
func (e *Engine) Ready() bool { return e.ready }

// SetupError returns the reason S3 preview is unavailable, if any.
func (e *Engine) SetupError() string { return e.readErr }

// Close closes the engine.
func (e *Engine) Close() error { return e.db.Close() }

// FromClause builds the DuckDB table expression that reads a single object.
func FromClause(bucket, key string, format model.Format) (string, error) {
	uri := fmt.Sprintf("s3://%s/%s", bucket, key)
	return readerFor(uri, format)
}

// GlobClause builds a table expression that reads every data file of a dataset
// under a prefix (used for dataset-level preview).
func GlobClause(bucket, prefix string, format model.Format) (string, error) {
	prefix = strings.TrimRight(prefix, "/")
	var pattern string
	switch format {
	case model.FormatParquet:
		pattern = "**/*.parquet"
	case model.FormatCSV:
		pattern = "**/*.csv"
	case model.FormatTSV:
		pattern = "**/*.tsv"
	case model.FormatJSON:
		pattern = "**/*.json"
	case model.FormatJSONL:
		pattern = "**/*.jsonl"
	default:
		pattern = "**/*"
	}
	uri := fmt.Sprintf("s3://%s/%s/%s", bucket, prefix, pattern)
	if prefix == "" {
		uri = fmt.Sprintf("s3://%s/%s", bucket, pattern)
	}
	return readerFor(uri, format)
}

// readerFor maps a format to the appropriate DuckDB read function.
func readerFor(uri string, format model.Format) (string, error) {
	switch format {
	case model.FormatParquet:
		return fmt.Sprintf("read_parquet('%s', union_by_name=true)", esc(uri)), nil
	case model.FormatCSV:
		return fmt.Sprintf("read_csv_auto('%s', sample_size=1000)", esc(uri)), nil
	case model.FormatTSV:
		return fmt.Sprintf("read_csv_auto('%s', delim='\\t', sample_size=1000)", esc(uri)), nil
	case model.FormatJSON, model.FormatJSONL:
		return fmt.Sprintf("read_json_auto('%s')", esc(uri)), nil
	case model.FormatIceberg:
		return fmt.Sprintf("iceberg_scan('%s')", esc(uri)), nil
	case model.FormatDelta:
		return fmt.Sprintf("delta_scan('%s')", esc(uri)), nil
	default:
		return "", fmt.Errorf("preview not supported for format %q", format)
	}
}

// PreviewFile previews a single object: up to `limit` rows plus column types.
func (e *Engine) PreviewFile(ctx context.Context, bucket, key string, format model.Format, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	from, err := FromClause(bucket, key, format)
	if err != nil {
		return nil, err
	}
	return e.query(ctx, fmt.Sprintf("SELECT * FROM %s", from), limit)
}

// PreviewDataset previews a dataset by globbing its data files.
func (e *Engine) PreviewDataset(ctx context.Context, bucket, prefix string, format model.Format, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	from, err := GlobClause(bucket, prefix, format)
	if err != nil {
		return nil, err
	}
	return e.query(ctx, fmt.Sprintf("SELECT * FROM %s", from), limit)
}

// Summarize runs DuckDB's SUMMARIZE over a table expression to produce
// per-column statistics (min/max/nulls/distinct/etc).
func (e *Engine) Summarize(ctx context.Context, from string) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("S3 preview unavailable: %s", e.readErr)
	}
	return e.query(ctx, fmt.Sprintf("SUMMARIZE SELECT * FROM %s", from), 500)
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

func esc(s string) string { return strings.ReplaceAll(s, "'", "''") }

func cleanErr(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s", msg)
}
