package dataviewer

import (
	"strings"
	"testing"

	"github.com/apexion/apexion/internal/duckdb"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want ColumnKind
	}{
		{"BIGINT", KindNumeric},
		{"INTEGER", KindNumeric},
		{"HUGEINT", KindNumeric},
		{"DECIMAL(18,2)", KindNumeric},
		{"DOUBLE", KindNumeric},
		{"VARCHAR", KindString},
		{"VARCHAR(255)", KindString},
		{"TEXT", KindString},
		{"BOOLEAN", KindBool},
		{"DATE", KindTemporal},
		{"TIMESTAMP", KindTemporal},
		{"TIMESTAMP WITH TIME ZONE", KindTemporal},
		{"INTERVAL", KindTemporal},
		{"UUID", KindOther},
		{"BLOB", KindOther},
		{"STRUCT(a INT)", KindOther},
	}
	for _, c := range cases {
		if got := classify(c.in); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent(`we"ird`); got != `"we""ird"` {
		t.Errorf("quoteIdent = %q", got)
	}
}

func TestBuildAndApplyStats(t *testing.T) {
	profiles := []ColumnProfile{
		{Name: "id", Kind: KindNumeric},
		{Name: "email", Kind: KindString},
	}
	sql, plan := buildStatsSQL(profiles, "read_parquet('s3://b/k')", 5000)

	for _, want := range []string{"count(*)", "approx_count_distinct", "quantile_cont", "length(", "LIMIT 5000"} {
		if !strings.Contains(sql, want) {
			t.Errorf("stats SQL missing %q:\n%s", want, sql)
		}
	}

	// One result row positionally aligned to plan.
	row := []string{
		"100",                  // count(*)
		"90", "90", "1", "500", // id: count, distinct, min, max
		"42.5", "10.1", "40", // id: avg, std, median
		"80", "80", "a@b.co", "z@y.co", // email: count, distinct, min, max
		"6", "20", "12.5", // email: minlen, maxlen, avglen
	}
	if len(row) != len(plan) {
		t.Fatalf("test row has %d cols, plan has %d", len(row), len(plan))
	}
	applyStatsRow(row, plan, profiles)

	id := profiles[0]
	if id.Count != 100 || id.NonNull != 90 || id.Distinct != 90 || id.Min != "1" || id.Max != "500" {
		t.Errorf("id scalars wrong: %+v", id)
	}
	if id.Mean != 42.5 || id.Std != 10.1 || id.Median != 40 {
		t.Errorf("id numeric stats wrong: %+v", id)
	}
	em := profiles[1]
	if em.Count != 100 || em.NonNull != 80 || em.Min != "a@b.co" || em.MinLen != 6 || em.MaxLen != 20 || em.AvgLen != 12.5 {
		t.Errorf("email stats wrong: %+v", em)
	}
}

func TestApplyTopN(t *testing.T) {
	profiles := []ColumnProfile{{Name: "c0"}, {Name: "c1"}}
	res := &duckdb.Result{Rows: [][]string{
		{"1", "x", "5"},
		{"1", "y", "3"},
		{"0", "a", "9"},
		{"9", "oob", "1"}, // out-of-range col ignored
	}}
	applyTopN(res, profiles)
	if len(profiles[0].TopValues) != 1 || profiles[0].TopValues[0].Value != "a" || profiles[0].TopValues[0].Count != 9 {
		t.Errorf("col0 top values wrong: %+v", profiles[0].TopValues)
	}
	if len(profiles[1].TopValues) != 2 {
		t.Errorf("col1 expected 2 top values, got %+v", profiles[1].TopValues)
	}
}

func TestHistogram(t *testing.T) {
	if h := histogram([]float64{5, 5, 5}, 4); h != nil {
		t.Errorf("constant values should yield nil histogram, got %+v", h)
	}
	if h := histogram([]float64{1}, 4); h != nil {
		t.Errorf("single value should yield nil histogram")
	}
	h := histogram([]float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 5)
	if len(h) != 5 {
		t.Fatalf("expected 5 bins, got %d", len(h))
	}
	var total int64
	for _, b := range h {
		total += b.Count
	}
	if total != 10 {
		t.Errorf("expected all 10 values binned, got %d", total)
	}
}

func TestDeriveBadges(t *testing.T) {
	uuids := []string{"550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440001"}

	cases := []struct {
		name  string
		p     ColumnProfile
		cells []string
		want  string
	}{
		{"candidate key", ColumnProfile{Count: 100, NonNull: 100, Distinct: 100, DistinctPct: 1}, nil, "Candidate Key"},
		{"constant", ColumnProfile{Count: 50, NonNull: 50, Distinct: 1}, nil, "Constant"},
		{"mostly null", ColumnProfile{Count: 100, NonNull: 30, NullPct: 0.7, Distinct: 30, DistinctPct: 1.0}, nil, "Mostly Null"},
		{"uuid", ColumnProfile{Kind: KindString, Count: 100, NonNull: 95, Distinct: 95, DistinctPct: 1.0}, uuids, "UUID"},
		{"high cardinality", ColumnProfile{Count: 100, NonNull: 60, Distinct: 58, DistinctPct: 0.96}, nil, "High Cardinality"},
	}
	for _, c := range cases {
		badges := Derive(&c.p, c.cells)
		if !hasBadge(badges, c.want) {
			t.Errorf("%s: expected badge %q, got %+v", c.name, c.want, badges)
		}
		if len(badges) > maxBadges {
			t.Errorf("%s: too many badges %+v", c.name, badges)
		}
	}
}

func hasBadge(badges []Badge, label string) bool {
	for _, b := range badges {
		if b.Label == label {
			return true
		}
	}
	return false
}
