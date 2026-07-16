package dataviewer

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/marcboeker/go-duckdb/v2"

	"github.com/apexion/apexion/internal/duckdb"
)

// TestProfilingSQLExecutes runs the generated Query A / Query B against a real
// in-memory DuckDB table (not S3) to prove the SQL is syntactically valid and the
// positional parsers line up — the string-only unit tests can't catch a bad
// function name or CTE-scoping mistake.
func TestProfilingSQLExecutes(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE data AS SELECT * FROM (VALUES
		(1, 'a@b.co', 'x'),
		(2, 'c@d.co', 'y'),
		(3, 'e@f.co', NULL),
		(4, 'g@h.co', NULL)
	) AS v(id, email, note)`)
	if err != nil {
		t.Fatalf("seed table: %v", err)
	}

	profiles := []ColumnProfile{
		{Name: "id", Kind: KindNumeric},
		{Name: "email", Kind: KindString},
		{Name: "note", Kind: KindString},
	}

	// Query A — scalar stats.
	statsSQL, plan := buildStatsSQL(profiles, "data", 5000)
	rowsA, err := db.Query(statsSQL)
	if err != nil {
		t.Fatalf("stats query failed: %v\nSQL: %s", err, statsSQL)
	}
	statRow := scanStringRow(t, rowsA)
	rowsA.Close()
	if len(statRow) != len(plan) {
		t.Fatalf("stats returned %d cols, plan has %d", len(statRow), len(plan))
	}
	applyStatsRow(statRow, plan, profiles)

	if profiles[0].Count != 4 || profiles[0].NonNull != 4 || profiles[0].Distinct != 4 {
		t.Errorf("id counts wrong: %+v", profiles[0])
	}
	if profiles[0].Median == 0 {
		t.Errorf("id median not computed: %+v", profiles[0])
	}
	if profiles[2].NonNull != 2 { // note has 2 non-null of 4
		t.Errorf("note NonNull = %d, want 2", profiles[2].NonNull)
	}
	if profiles[1].MaxLen != 6 { // "a@b.co" length 6
		t.Errorf("email MaxLen = %d, want 6", profiles[1].MaxLen)
	}

	// Query B — top-N (string columns only).
	topSQL, ok := buildTopNSQL(profiles, "data", 5000)
	if !ok {
		t.Fatal("expected top-N query for string columns")
	}
	rowsB, err := db.Query(topSQL)
	if err != nil {
		t.Fatalf("top-N query failed: %v\nSQL: %s", err, topSQL)
	}
	res := scanResult(t, rowsB)
	rowsB.Close()
	applyTopN(&res, profiles)
	if len(profiles[1].TopValues) == 0 {
		t.Errorf("expected top values for email, got none")
	}
	// id is numeric: no top-N branch, so no TopValues.
	if len(profiles[0].TopValues) != 0 {
		t.Errorf("numeric column should have no top-N values: %+v", profiles[0].TopValues)
	}
}

// scanStringRow reads a single result row, stringifying each cell the way the
// engine does.
func scanStringRow(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	cols, _ := rows.Columns()
	if !rows.Next() {
		t.Fatal("expected one result row")
	}
	cells := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range cells {
		ptrs[i] = &cells[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return stringifyAny(cells)
}

func scanResult(t *testing.T, rows *sql.Rows) duckdb.Result {
	t.Helper()
	cols, _ := rows.Columns()
	out := duckdb.Result{}
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out.Rows = append(out.Rows, stringifyAny(cells))
	}
	return out
}

func stringifyAny(cells []any) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		switch x := c.(type) {
		case nil:
			out[i] = ""
		case []byte:
			out[i] = string(x)
		default:
			out[i] = fmt.Sprintf("%v", x)
		}
	}
	return out
}
