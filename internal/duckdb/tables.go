package duckdb

import (
	"fmt"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// This file builds the DuckDB FROM-clause / table expressions that turn a
// directory of object-storage files (CSV/Parquet/JSON/JSONL) into a queryable
// table — the core of directory-table creation.

func FromClause(bucket, key string, format model.Format, opts model.ReadOptions) (string, error) {
	uri := fmt.Sprintf("s3://%s/%s", bucket, key)
	return readerFor(uri, format, opts)
}

// globClause builds a table expression that reads every data file of a dataset
// under a prefix. DuckDB's S3 glob does not support brace expansion, so the
// pattern carries a single extension. When a representative object key is known,
// its real extension (including any compression suffix, e.g. .csv.gz) is used so
// compressed datasets are matched; otherwise it falls back to the plain
// per-format pattern.
func globClause(bucket, prefix string, format model.Format, sampleKey string, opts model.ReadOptions) (string, error) {
	return globWith(bucket, prefix, globPattern(format, sampleKey), format, opts)
}

// globPattern returns the recursive glob for a dataset. A representative object
// key yields the file's true extension including compression (part-0.csv.gz ->
// **/*.csv.gz); with no sample it falls back to the plain per-format pattern.
func globPattern(format model.Format, sampleKey string) string {
	if sampleKey != "" {
		return "**/*" + dataExtension(sampleKey, format)
	}
	return defaultPattern(format)
}

func globWith(bucket, prefix, pattern string, format model.Format, opts model.ReadOptions) (string, error) {
	prefix = strings.TrimRight(prefix, "/")
	uri := fmt.Sprintf("s3://%s/%s/%s", bucket, prefix, pattern)
	if prefix == "" {
		uri = fmt.Sprintf("s3://%s/%s", bucket, pattern)
	}
	return readerExprOpts("'"+esc(uri)+"'", format, true, opts)
}

// partitionGlobReader builds a reader that also exposes the source path as a
// `filename` column (and disables hive_partitioning, since bare-directory
// datasets have no key=value segments). The catalog derives virtual partition
// columns from `filename` via split_part.
func partitionGlobReader(bucket, prefix string, format model.Format, sampleKey string, opts model.ReadOptions) (string, error) {
	prefix = strings.TrimRight(prefix, "/")
	pattern := globPattern(format, sampleKey)
	uri := fmt.Sprintf("s3://%s/%s/%s", bucket, prefix, pattern)
	if prefix == "" {
		uri = fmt.Sprintf("s3://%s/%s", bucket, pattern)
	}
	// A positional view derives its partition columns from `filename` via
	// split_part, so the reader MUST expose it regardless of the user's choice.
	// The VIEW (partitionViewSQL) decides whether to keep it in the output.
	ro := opts
	ro.Filename = true
	return readerExprOpts("'"+esc(uri)+"'", format, false, ro)
}

// defaultPattern is the plain (uncompressed) recursive glob for a format.
func defaultPattern(format model.Format) string {
	switch format {
	case model.FormatParquet:
		return "**/*.parquet"
	case model.FormatCSV:
		return "**/*.csv"
	case model.FormatTSV:
		return "**/*.tsv"
	case model.FormatJSON:
		return "**/*.json"
	case model.FormatJSONL:
		return "**/*.jsonl"
	default:
		return "**/*"
	}
}

// dataExtension returns the data-file extension of a key including any
// compression suffix, e.g. "part-0.csv.gz" -> ".csv.gz". Falls back to the
// format's canonical extension when the base token is absent.
func dataExtension(key string, format model.Format) string {
	name := key
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	base := formatToken(format)
	if base == "" {
		return defaultExt(format)
	}
	lower := strings.ToLower(name)
	if i := strings.Index(lower, "."+base); i >= 0 {
		return name[i:]
	}
	return defaultExt(format)
}

func formatToken(format model.Format) string {
	switch format {
	case model.FormatParquet:
		return "parquet"
	case model.FormatCSV:
		return "csv"
	case model.FormatTSV:
		return "tsv"
	case model.FormatJSON:
		return "json"
	case model.FormatJSONL:
		return "jsonl"
	default:
		return ""
	}
}

func defaultExt(format model.Format) string {
	if t := formatToken(format); t != "" {
		return "." + t
	}
	return ""
}

// readerExpr maps a format to its DuckDB read function around an already-built
// path expression (a quoted URI, a glob, or a list literal), applying the
// caller's per-dataset read options with no hive_partitioning.
func readerExpr(pathExpr string, format model.Format, opts model.ReadOptions) (string, error) {
	return readerExprOpts(pathExpr, format, false, opts)
}

// readerExprOpts builds the DuckDB reader for a path expression. `hive` is
// structural: it enables hive_partitioning=true (so year=2026/month=07 keys
// surface as columns) and comes from discovery, not the user. `opts` are the
// user's per-dataset read options (filename, union_by_name, header, sample_size,
// ignore_errors). CSV-only options (header/sample_size) are ignored for
// parquet/json readers, which do not accept them.
func readerExprOpts(pathExpr string, format model.Format, hive bool, opts model.ReadOptions) (string, error) {
	// Trailing options common to the glob readers, appended in a stable order.
	tail := ""
	if opts.Filename {
		tail += ", filename=true"
	}
	if hive {
		tail += ", hive_partitioning=true"
	}
	switch format {
	case model.FormatParquet:
		opt := ""
		if opts.UnionByName {
			opt += ", union_by_name=true"
		}
		return fmt.Sprintf("read_parquet(%s%s%s)", pathExpr, opt, tail), nil
	case model.FormatCSV:
		return fmt.Sprintf("read_csv_auto(%s%s%s)", pathExpr, csvOpts(opts), tail), nil
	case model.FormatTSV:
		return fmt.Sprintf("read_csv_auto(%s, delim='\\t'%s%s)", pathExpr, csvOpts(opts), tail), nil
	case model.FormatJSON, model.FormatJSONL:
		opt := ""
		if opts.UnionByName {
			opt += ", union_by_name=true"
		}
		if opts.IgnoreErrors {
			opt += ", ignore_errors=true"
		}
		return fmt.Sprintf("read_json_auto(%s%s%s)", pathExpr, opt, tail), nil
	case model.FormatIceberg:
		return fmt.Sprintf("iceberg_scan(%s)", pathExpr), nil
	case model.FormatDelta:
		return fmt.Sprintf("delta_scan(%s)", pathExpr), nil
	default:
		return "", fmt.Errorf("preview not supported for format %q", format)
	}
}

// csvOpts builds the read_csv_auto option suffix (excluding delim/filename/hive)
// from the user's read options. sample_size preserves the historical 1000-row
// default; header "auto" is omitted so DuckDB auto-detects it.
func csvOpts(opts model.ReadOptions) string {
	opt := ""
	switch {
	case opts.SampleSize == 0:
		opt += ", sample_size=1000"
	default:
		opt += fmt.Sprintf(", sample_size=%d", opts.SampleSize)
	}
	if opts.UnionByName {
		opt += ", union_by_name=true"
	}
	switch opts.Header {
	case "present":
		opt += ", header=true"
	case "none":
		opt += ", header=false"
	}
	if lit := csvDelimLiteral(opts.Delimiter); lit != "" {
		opt += fmt.Sprintf(", delim='%s'", esc(lit))
	}
	if opts.IgnoreErrors {
		opt += ", ignore_errors=true"
	}
	return opt
}

// csvDelimLiteral maps a delimiter option to the DuckDB delim string literal
// (unquoted), or "" for auto-detect. Only a known set is honoured so an
// arbitrary value can never reach the generated SQL. The TSV reader sets its own
// delim, so a delimiter is only ever offered for CSV in the UI.
func csvDelimLiteral(d string) string {
	switch d {
	case "tab":
		return `\t`
	case "space":
		return " "
	case ",", ";", "|", ":":
		return d
	default:
		return ""
	}
}

// readerFor builds a reader over a single object URI (used by FromClause).
func readerFor(uri string, format model.Format, opts model.ReadOptions) (string, error) {
	return readerExpr("'"+esc(uri)+"'", format, opts)
}

// ListReader builds a DuckDB reader over an explicit list of object URIs, e.g.
// read_parquet(['s3://b/f1.parquet','s3://b/f2.parquet']). DuckDB's read_*
// functions accept a list literal natively, so a multi-file virtual table is
// just the normal per-format reader wrapped around a list expression — no
// globbing, no catalog. Hive partitioning is off (an ad-hoc file list has no
// key=value layout); the caller's ReadOptions (header, union_by_name,
// ignore_errors, sample_size, filename) still apply. This is the single home
// for multi-file reader syntax, so new source formats plug in via the same
// per-format switch (see readerExprOpts).
func ListReader(uris []string, format model.Format, opts model.ReadOptions) (string, error) {
	if len(uris) == 0 {
		return "", fmt.Errorf("no files selected")
	}
	quoted := make([]string, len(uris))
	for i, u := range uris {
		quoted[i] = "'" + esc(u) + "'"
	}
	list := "[" + strings.Join(quoted, ", ") + "]"
	return readerExprOpts(list, format, false, opts)
}

// PreviewFile previews a single object: up to `limit` rows plus column types.
