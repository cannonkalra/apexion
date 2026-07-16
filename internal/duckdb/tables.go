package duckdb

import (
	"fmt"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// This file builds the DuckDB FROM-clause / table expressions that turn a
// directory of object-storage files (CSV/Parquet/JSON/JSONL) into a queryable
// table — the core of directory-table creation.

func FromClause(bucket, key string, format model.Format) (string, error) {
	uri := fmt.Sprintf("s3://%s/%s", bucket, key)
	return readerFor(uri, format)
}

// globClause builds a table expression that reads every data file of a dataset
// under a prefix. DuckDB's S3 glob does not support brace expansion, so this
// matches a single (uncompressed) extension. For compressed files, or to be
// exact, prefer ReaderForFiles / GlobClauseFromKey which derive the real
// extension from the cataloged objects.
func globClause(bucket, prefix string, format model.Format) (string, error) {
	return globWith(bucket, prefix, defaultPattern(format), format)
}

func globWith(bucket, prefix, pattern string, format model.Format) (string, error) {
	prefix = strings.TrimRight(prefix, "/")
	uri := fmt.Sprintf("s3://%s/%s/%s", bucket, prefix, pattern)
	if prefix == "" {
		uri = fmt.Sprintf("s3://%s/%s", bucket, pattern)
	}
	return readerExpr("'"+esc(uri)+"'", format)
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
// path expression (a quoted URI, a glob, or a list literal).
func readerExpr(pathExpr string, format model.Format) (string, error) {
	switch format {
	case model.FormatParquet:
		return fmt.Sprintf("read_parquet(%s, union_by_name=true)", pathExpr), nil
	case model.FormatCSV:
		return fmt.Sprintf("read_csv_auto(%s, sample_size=1000, union_by_name=true)", pathExpr), nil
	case model.FormatTSV:
		return fmt.Sprintf("read_csv_auto(%s, delim='\\t', sample_size=1000, union_by_name=true)", pathExpr), nil
	case model.FormatJSON, model.FormatJSONL:
		return fmt.Sprintf("read_json_auto(%s)", pathExpr), nil
	case model.FormatIceberg:
		return fmt.Sprintf("iceberg_scan(%s)", pathExpr), nil
	case model.FormatDelta:
		return fmt.Sprintf("delta_scan(%s)", pathExpr), nil
	default:
		return "", fmt.Errorf("preview not supported for format %q", format)
	}
}

// readerFor builds a reader over a single object URI (used by FromClause).
func readerFor(uri string, format model.Format) (string, error) {
	return readerExpr("'"+esc(uri)+"'", format)
}

// PreviewFile previews a single object: up to `limit` rows plus column types.
