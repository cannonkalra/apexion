package compat

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		fam  Family
		want string // String() round-trip (canonical)
	}{
		{"INTEGER", FamInt, "INTEGER"},
		{"BIGINT", FamInt, "BIGINT"},
		{"UINTEGER", FamInt, "UINTEGER"},
		{"DOUBLE", FamFloat, "DOUBLE"},
		{"FLOAT", FamFloat, "FLOAT"},
		{"VARCHAR", FamString, "VARCHAR"},
		{"STRING", FamString, "VARCHAR"},
		{"DECIMAL(18,2)", FamDecimal, "DECIMAL(18,2)"},
		{"TIMESTAMP", FamTimestamp, "TIMESTAMP"},
		{"TIMESTAMP WITH TIME ZONE", FamTimestamp, "TIMESTAMP WITH TIME ZONE"},
		{"DATE", FamDate, "DATE"},
		{"BOOLEAN", FamBool, "BOOLEAN"},
		{"UUID", FamUUID, "UUID"},
		{"JSON", FamJSON, "JSON"},
		{"INTEGER[]", FamList, "INTEGER[]"},
		{"VARCHAR[]", FamList, "VARCHAR[]"},
		{"STRUCT(a INTEGER, b VARCHAR)", FamStruct, "STRUCT(a INTEGER, b VARCHAR)"},
		{"MAP(VARCHAR, INTEGER)", FamMap, "MAP(VARCHAR, INTEGER)"},
		{"", FamUnknown, "NULL"},
		{"NULL", FamUnknown, "NULL"},
	}
	for _, c := range cases {
		got := Normalize(c.in)
		if got.Fam != c.fam {
			t.Errorf("Normalize(%q).Fam = %v, want %v", c.in, got.Fam, c.fam)
		}
		if s := got.String(); s != c.want {
			t.Errorf("Normalize(%q).String() = %q, want %q", c.in, s, c.want)
		}
	}
}

func TestCompatible(t *testing.T) {
	var def Options
	cases := []struct {
		a, b        string
		ok, widened bool
		to          string
	}{
		// Safe promotions (✓, not widened).
		{"INTEGER", "BIGINT", true, false, "BIGINT"},
		{"TINYINT", "INTEGER", true, false, "INTEGER"},
		{"VARCHAR", "STRING", true, false, "VARCHAR"},
		{"TIMESTAMP", "TIMESTAMP", true, false, "TIMESTAMP"},
		// Widenings (⚠).
		{"INTEGER", "DOUBLE", true, true, "DOUBLE"},
		{"DATE", "TIMESTAMP", true, true, "TIMESTAMP"},
		{"TIMESTAMP", "TIMESTAMP WITH TIME ZONE", true, true, "TIMESTAMP WITH TIME ZONE"},
		{"INTEGER", "DECIMAL(18,2)", true, true, "DECIMAL(18,2)"},
		// All-null column casts to anything.
		{"NULL", "VARCHAR", true, false, "VARCHAR"},
		{"BIGINT", "NULL", true, false, "BIGINT"},
		// Incompatible (✕).
		{"DOUBLE", "VARCHAR", false, false, ""},
		{"BOOLEAN", "TINYINT", false, false, ""}, // balanced default: not coerced
		{"INTEGER[]", "VARCHAR[]", false, false, ""},
	}
	for _, c := range cases {
		r := Compatible(Normalize(c.a), Normalize(c.b), def)
		if r.OK != c.ok {
			t.Errorf("Compatible(%q,%q).OK = %v, want %v", c.a, c.b, r.OK, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		if r.Widened != c.widened {
			t.Errorf("Compatible(%q,%q).Widened = %v, want %v", c.a, c.b, r.Widened, c.widened)
		}
		if got := r.To.String(); got != c.to {
			t.Errorf("Compatible(%q,%q).To = %q, want %q", c.a, c.b, got, c.to)
		}
	}
}

func TestCompatibleBoolIntCoerce(t *testing.T) {
	r := Compatible(Normalize("BOOLEAN"), Normalize("TINYINT"), Options{BoolIntCoerce: true})
	if !r.OK || !r.Widened {
		t.Errorf("bool+tinyint with coerce = %+v, want OK widened", r)
	}
}

// helper: build a FileSchema from name→type pairs.
func fs(key string, pairs ...string) FileSchema {
	f := FileSchema{Ref: FileRef{Bucket: "b", Key: key, Format: "parquet"}}
	for i := 0; i+1 < len(pairs); i += 2 {
		f.Cols = append(f.Cols, Column{Name: pairs[i], Type: Normalize(pairs[i+1])})
	}
	return f
}

func TestGroupAllCompatible(t *testing.T) {
	g := Group([]FileSchema{
		fs("a.parquet", "id", "INTEGER", "name", "VARCHAR"),
		fs("b.parquet", "id", "BIGINT", "name", "VARCHAR"),
	}, Options{})
	if len(g.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(g.Members))
	}
	if len(g.Rejected) != 0 {
		t.Fatalf("rejected = %v, want none", g.Rejected)
	}
	if g.Merged[0].Type.String() != "BIGINT" {
		t.Errorf("merged id type = %q, want BIGINT", g.Merged[0].Type.String())
	}
}

func TestGroupIncompatibleType(t *testing.T) {
	// orders_03 price DOUBLE, orders_04 price VARCHAR → orders_04 rejected.
	g := Group([]FileSchema{
		fs("orders_03.parquet", "id", "INTEGER", "price", "DOUBLE"),
		fs("orders_04.parquet", "id", "INTEGER", "price", "VARCHAR"),
	}, Options{})
	if len(g.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(g.Members))
	}
	if len(g.Rejected) != 1 || g.Rejected[0].Kind != KindType || g.Rejected[0].Column != "price" {
		t.Fatalf("rejected = %+v, want one type reason on price", g.Rejected)
	}
}

func TestGroupShapeMismatch(t *testing.T) {
	g := Group([]FileSchema{
		fs("a.parquet", "id", "INTEGER", "name", "VARCHAR"),
		fs("b.parquet", "id", "INTEGER", "name", "VARCHAR"),
		fs("c.parquet", "id", "INTEGER"), // one column: wrong shape
	}, Options{})
	if len(g.Members) != 2 {
		t.Fatalf("members = %d, want 2 (modal shape wins)", len(g.Members))
	}
	if len(g.Rejected) != 1 || g.Rejected[0].Kind != KindShape {
		t.Fatalf("rejected = %+v, want one shape reason", g.Rejected)
	}
}

func TestGroupWidenWarning(t *testing.T) {
	g := Group([]FileSchema{
		fs("a.parquet", "t", "DATE"),
		fs("b.parquet", "t", "TIMESTAMP"),
	}, Options{})
	if len(g.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(g.Members))
	}
	if len(g.Warnings) != 1 || g.Warnings[0].Kind != KindWiden {
		t.Fatalf("warnings = %+v, want one widen warning", g.Warnings)
	}
	if !g.Merged[0].Widened || g.Merged[0].Type.String() != "TIMESTAMP" {
		t.Errorf("merged col = %+v, want widened TIMESTAMP", g.Merged[0])
	}
}
