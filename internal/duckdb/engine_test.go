package duckdb

import (
	"strings"
	"testing"

	"github.com/apexion/apexion/internal/model"
)

func TestDataExtension(t *testing.T) {
	cases := []struct {
		key    string
		format model.Format
		want   string
	}{
		{"2025-02-22/AU/AAID/part-0.csv.gz", model.FormatCSV, ".csv.gz"},
		{"a/b/part-0.csv", model.FormatCSV, ".csv"},
		{"x/data.csv.zst", model.FormatCSV, ".csv.zst"},
		{"x/data.tsv.bz2", model.FormatTSV, ".tsv.bz2"},
		{"events/e.jsonl.gz", model.FormatJSONL, ".jsonl.gz"},
		{"t/part-0.parquet", model.FormatParquet, ".parquet"},
		{"weird/name-without-ext", model.FormatCSV, ".csv"}, // fallback to canonical
	}
	for _, c := range cases {
		if got := dataExtension(c.key, c.format); got != c.want {
			t.Errorf("dataExtension(%q,%s)=%q want %q", c.key, c.format, got, c.want)
		}
	}
}

func TestGlobClauseNoBraces(t *testing.T) {
	// DuckDB's S3 glob does not support brace expansion; the plain glob must not
	// contain one.
	from, _ := globClause("b", "p", model.FormatCSV)
	if strings.ContainsAny(from, "{}") {
		t.Errorf("plain glob must not use brace expansion: %s", from)
	}
	if !strings.Contains(from, "**/*.csv") {
		t.Errorf("unexpected plain glob: %s", from)
	}
}

func TestFromClause(t *testing.T) {
	from, err := FromClause("bucket", "dir/part-0.parquet", model.FormatParquet)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(from, "read_parquet(") {
		t.Errorf("wrong reader: %s", from)
	}
	if !strings.Contains(from, "s3://bucket/dir/part-0.parquet") {
		t.Errorf("missing object path: %s", from)
	}
	if _, err := FromClause("b", "x", model.FormatORC); err == nil {
		t.Error("expected unsupported-format error for ORC")
	}
}
