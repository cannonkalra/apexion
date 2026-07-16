package duckdb

import (
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

// TestGlobClauseExposesPartitions verifies the glob/view reader enables
// hive_partitioning so partition directories (year=/month=) become SQL columns.
func TestGlobClauseExposesPartitions(t *testing.T) {
	from, err := globClause("logs", "events", model.FormatParquet)
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
	single, _ := FromClause("logs", "events/part-0.parquet", model.FormatParquet)
	if strings.Contains(single, "hive_partitioning") {
		t.Errorf("single-file reader should not enable hive_partitioning: %s", single)
	}
}
