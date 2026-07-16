package dataviewer

import "strings"

// classify maps a DuckDB type name (as reported by ColumnType.DatabaseTypeName,
// e.g. "BIGINT", "VARCHAR", "DECIMAL(18,2)", "TIMESTAMP WITH TIME ZONE") to a
// ColumnKind. Unknown/nested types fall through to KindOther, which still gets
// count/distinct/min/max but no kind-specific aggregates.
func classify(dtype string) ColumnKind {
	t := strings.ToUpper(strings.TrimSpace(dtype))
	// Strip any parameterisation: DECIMAL(18,2) -> DECIMAL, VARCHAR(10) -> VARCHAR.
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}

	switch t {
	case "BOOLEAN", "BOOL", "LOGICAL":
		return KindBool
	case "VARCHAR", "CHAR", "BPCHAR", "TEXT", "STRING":
		return KindString
	}

	switch {
	case strings.HasPrefix(t, "DATE"),
		strings.HasPrefix(t, "TIME"), // TIME, TIMESTAMP, TIMESTAMPTZ, TIMESTAMP WITH...
		strings.HasPrefix(t, "INTERVAL"):
		return KindTemporal
	}

	switch t {
	case "TINYINT", "SMALLINT", "INTEGER", "INT", "INT1", "INT2", "INT4", "INT8",
		"BIGINT", "HUGEINT", "UHUGEINT",
		"UTINYINT", "USMALLINT", "UINTEGER", "UBIGINT",
		"FLOAT", "REAL", "FLOAT4", "FLOAT8", "DOUBLE", "DECIMAL", "NUMERIC":
		return KindNumeric
	}

	return KindOther
}

// isNumeric / isString are convenience predicates over Kind.
func (k ColumnKind) isNumeric() bool { return k == KindNumeric }
func (k ColumnKind) isString() bool  { return k == KindString }

// quoteIdent double-quotes a DuckDB identifier, escaping embedded quotes, so an
// arbitrary column name can be interpolated safely into a projection.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
