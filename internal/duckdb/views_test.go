package duckdb

import (
	"fmt"
	"strings"
	"testing"

	"github.com/apexion/apexion/internal/model"
)

func TestValidIdentifier(t *testing.T) {
	ok := []string{"events", "logs_2026", "t1", "_hidden"}
	bad := []string{"2026logs", "my-table", "drop table", "a.b", "", "sel;ect"}
	for _, s := range ok {
		if !ValidIdentifier(s) {
			t.Errorf("ValidIdentifier(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidIdentifier(s) {
			t.Errorf("ValidIdentifier(%q) = true, want false", s)
		}
	}
}

func TestReadOnlyGuard(t *testing.T) {
	allowed := []string{
		"SELECT * FROM events",
		"  with x as (select 1) select * from x",
		"DESCRIBE events",
		"SUMMARIZE events",
		"(SELECT 1)",
	}
	blocked := []string{
		"DROP VIEW events",
		"INSERT INTO events VALUES (1)",
		"CREATE TABLE t (a int)",
		"DELETE FROM events",
		"UPDATE events SET a=1",
		"ATTACH 'x.db'",
		"SELECT 1; DROP TABLE x",              // multi-statement bypass
		"SELECT * FROM events; DELETE FROM t", // multi-statement bypass
	}
	for _, s := range allowed {
		if err := readOnly(s); err != nil {
			t.Errorf("readOnly(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range blocked {
		if err := readOnly(s); err == nil {
			t.Errorf("readOnly(%q) = nil, want error", s)
		}
	}
}

// TestPartitionViewSQL verifies the virtual-partition view derives pt columns
// from the object path via split_part (no regex) and reads the original objects.
func TestPartitionViewSQL(t *testing.T) {
	opts := model.DefaultReadOptions()
	opts.Filename = false // default positional view drops the filename column
	sql, err := partitionViewSQL(viewRef(false, "idfa"), "eyeota-data-feed", "IDFA", model.FormatParquet, "", []string{"pt0", "pt1", "pt2"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	// prefix "s3://eyeota-data-feed/IDFA/" has length 27; substr starts at 28.
	wantPrefixLen := len("s3://eyeota-data-feed/IDFA/") + 1
	for i, name := range []string{"pt0", "pt1", "pt2"} {
		frag := fmt.Sprintf(`split_part(substr(filename, %d), '/', %d) AS "%s"`, wantPrefixLen, i+1, name)
		if !strings.Contains(sql, frag) {
			t.Errorf("missing derived column %q in:\n%s", frag, sql)
		}
	}
	for _, must := range []string{`CREATE OR REPLACE VIEW "idfa"`, "* EXCLUDE (filename)", "filename=true", "read_parquet("} {
		if !strings.Contains(sql, must) {
			t.Errorf("SQL missing %q:\n%s", must, sql)
		}
	}
	if strings.Contains(sql, "hive_partitioning") {
		t.Errorf("virtual-partition view must not use hive_partitioning:\n%s", sql)
	}
	// No partition names → plain path (CreateView) is used; partitionViewSQL is
	// only reached with names, so an empty-name slice yields no derived columns.
	empty, _ := partitionViewSQL(viewRef(false, "t"), "b", "p", model.FormatCSV, "", nil, opts)
	if strings.Contains(empty, "split_part") {
		t.Errorf("no partition names should mean no split_part: %s", empty)
	}
}

// TestPartitionViewFilename verifies the positional view keeps the filename
// column (no EXCLUDE) when the user opts to expose it, and drops it otherwise —
// while the reader always exposes filename so split_part can derive columns.
func TestPartitionViewFilename(t *testing.T) {
	on := model.DefaultReadOptions() // Filename=true
	sql, err := partitionViewSQL(viewRef(false, "idfa"), "b", "root", model.FormatCSV, "", []string{"pt0"}, on)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "EXCLUDE (filename)") {
		t.Errorf("positional view with Filename=true must NOT EXCLUDE filename:\n%s", sql)
	}
	if !strings.Contains(sql, "filename=true") {
		t.Errorf("reader must still expose filename to derive partitions:\n%s", sql)
	}
	off := model.DefaultReadOptions()
	off.Filename = false
	sql, err = partitionViewSQL(viewRef(false, "idfa"), "b", "root", model.FormatCSV, "", []string{"pt0"}, off)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "EXCLUDE (filename)") {
		t.Errorf("positional view with Filename=false must EXCLUDE filename:\n%s", sql)
	}
}

// TestCSVReaderOptions verifies the per-dataset read options thread into the
// read_csv_auto option string.
func TestCSVReaderOptions(t *testing.T) {
	none := model.ReadOptions{Header: "none"}
	r, err := readerExprOpts("'x'", model.FormatCSV, false, none)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r, "header=false") {
		t.Errorf("Header=none should emit header=false: %s", r)
	}

	union := model.ReadOptions{UnionByName: true, Filename: true}
	r, err = readerExprOpts("'x'", model.FormatCSV, false, union)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r, "union_by_name=true") {
		t.Errorf("UnionByName should emit union_by_name=true: %s", r)
	}
	if !strings.Contains(r, "filename=true") {
		t.Errorf("Filename should emit filename=true: %s", r)
	}

	// Auto header is omitted so DuckDB auto-detects it.
	auto := model.DefaultReadOptions()
	r, _ = readerExprOpts("'x'", model.FormatCSV, false, auto)
	if strings.Contains(r, "header=") {
		t.Errorf("auto header must be omitted: %s", r)
	}
}

// TestGlobClauseExposesPartitions verifies the glob/view reader enables
// hive_partitioning so partition directories (year=/month=) become SQL columns.
func TestGlobClauseExposesPartitions(t *testing.T) {
	from, err := globClause("logs", "events", model.FormatParquet, "", model.DefaultReadOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(from, "hive_partitioning=true") {
		t.Errorf("view reader must enable hive_partitioning for partition columns: %s", from)
	}
	if !strings.Contains(from, "s3://logs/events/**/*.parquet") {
		t.Errorf("unexpected glob: %s", from)
	}
	// Single-file preview must NOT enable hive_partitioning (behavior preserved).
	single, _ := FromClause("logs", "events/part-0.parquet", model.FormatParquet, model.DefaultReadOptions())
	if strings.Contains(single, "hive_partitioning") {
		t.Errorf("single-file reader should not enable hive_partitioning: %s", single)
	}
}
