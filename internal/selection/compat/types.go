// Package compat is the compatibility engine for multi-file selections: given
// the per-file column schemas discovered from object storage, it decides which
// files can be read together as one virtual table and why the rest cannot.
//
// It is a pure, self-contained package — stdlib plus internal/model only, no
// DuckDB, no I/O, no regex — mirroring internal/catalog/virtualpath. Everything
// here is unit-testable without a live engine.
//
// The core idea is a normalized type lattice: DuckDB's DESCRIBE reports types as
// strings (INTEGER, VARCHAR, DECIMAL(18,2), TIMESTAMP WITH TIME ZONE, INT[],
// STRUCT(a INTEGER, b VARCHAR)). Normalize collapses those into a Family + a few
// attributes so Compatible can reason about widening (int32+int64 → ok,
// date+timestamp → ok-with-widening, DOUBLE vs VARCHAR → incompatible) instead
// of string-matching.
package compat

import (
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// Family is the coarse type category two columns must share (with a few
// cross-family widenings) to be compatible.
type Family int

const (
	FamUnknown Family = iota // NULL / all-null columns: compatible with anything
	FamBool
	FamInt
	FamFloat
	FamDecimal
	FamString
	FamBlob
	FamDate
	FamTime
	FamTimestamp
	FamInterval
	FamUUID
	FamJSON
	FamList
	FamStruct
	FamMap
)

// NormType is a DuckDB column type reduced to a comparable, widening-aware form.
// Only the fields relevant to a family are set (Bits/Signed for Int, Prec/Scale
// for Decimal, Elem for List, Fields for Struct, Key/Val for Map, TZ for
// time/timestamp). Raw preserves the original DESCRIBE string for messages.
type NormType struct {
	Fam    Family
	Bits   int // Int/Float width in bits
	Signed bool
	Prec   int // Decimal precision
	Scale  int // Decimal scale
	TZ     bool
	Elem   *NormType // List element
	Key    *NormType // Map key
	Val    *NormType // Map value
	Fields []Field   // Struct fields (order-significant)
	Raw    string
}

// Field is one named member of a struct type.
type Field struct {
	Name string
	Type NormType
}

// String renders a NormType back to a canonical DuckDB-ish type name, used in
// compatibility reasons and the schema preview. Merged types (with no Raw) get
// a synthesized name; leaf types with a Raw prefer it.
func (n NormType) String() string {
	switch n.Fam {
	case FamUnknown:
		if n.Raw != "" {
			return n.Raw
		}
		return "NULL"
	case FamBool:
		return "BOOLEAN"
	case FamInt:
		name := map[int]string{8: "TINYINT", 16: "SMALLINT", 32: "INTEGER", 64: "BIGINT", 128: "HUGEINT"}[n.Bits]
		if name == "" {
			name = "BIGINT"
		}
		if !n.Signed {
			return "U" + name
		}
		return name
	case FamFloat:
		if n.Bits == 32 {
			return "FLOAT"
		}
		return "DOUBLE"
	case FamDecimal:
		return "DECIMAL(" + itoa(n.Prec) + "," + itoa(n.Scale) + ")"
	case FamString:
		return "VARCHAR"
	case FamBlob:
		return "BLOB"
	case FamDate:
		return "DATE"
	case FamTime:
		if n.TZ {
			return "TIME WITH TIME ZONE"
		}
		return "TIME"
	case FamTimestamp:
		if n.TZ {
			return "TIMESTAMP WITH TIME ZONE"
		}
		return "TIMESTAMP"
	case FamInterval:
		return "INTERVAL"
	case FamUUID:
		return "UUID"
	case FamJSON:
		return "JSON"
	case FamList:
		return deref(n.Elem).String() + "[]"
	case FamMap:
		return "MAP(" + deref(n.Key).String() + ", " + deref(n.Val).String() + ")"
	case FamStruct:
		parts := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			parts[i] = f.Name + " " + f.Type.String()
		}
		return "STRUCT(" + strings.Join(parts, ", ") + ")"
	}
	if n.Raw != "" {
		return n.Raw
	}
	return "UNKNOWN"
}

// itoa is a tiny non-negative int formatter (keeps the package strconv-free like
// virtualpath).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Column pairs a column name with its normalized type.
type Column struct {
	Name string
	Type NormType
}

// FileRef identifies a selected object. It is defined here (the lowest layer)
// so both the selection service and the SQL generator can alias it without an
// import cycle.
type FileRef struct {
	Bucket string       `json:"bucket"`
	Key    string       `json:"key"`
	Format model.Format `json:"format"`
	Size   int64        `json:"size"`
}

// URI returns the s3:// address of the file.
func (f FileRef) URI() string { return "s3://" + f.Bucket + "/" + f.Key }

// Normalize reduces a DuckDB DESCRIBE type string to a NormType. It is tolerant:
// an unrecognized string falls back to FamUnknown carrying the raw text, which
// Compatible treats as castable to anything (so an odd type never crashes a
// selection — it just widens).
func Normalize(duck string) NormType {
	raw := strings.TrimSpace(duck)
	s := strings.ToUpper(raw)
	nt := NormType{Raw: raw}

	// List: a trailing [] or [N] wraps an element type.
	if i := strings.LastIndex(s, "["); i > 0 && strings.HasSuffix(s, "]") {
		elem := Normalize(raw[:i])
		return NormType{Fam: FamList, Elem: &elem, Raw: raw}
	}

	switch {
	case s == "" || s == "NULL" || s == "SQLNULL":
		nt.Fam = FamUnknown
	case s == "BOOLEAN" || s == "BOOL" || s == "LOGICAL":
		nt.Fam = FamBool
	case strings.HasPrefix(s, "DECIMAL") || strings.HasPrefix(s, "NUMERIC"):
		nt.Fam = FamDecimal
		nt.Prec, nt.Scale = parseDecimal(s)
	case strings.HasPrefix(s, "STRUCT"):
		nt.Fam = FamStruct
		nt.Fields = parseStructFields(raw)
	case strings.HasPrefix(s, "MAP"):
		nt.Fam = FamMap
		k, v := parseMapKV(raw)
		nt.Key, nt.Val = &k, &v
	case strings.HasPrefix(s, "TIMESTAMP"):
		nt.Fam = FamTimestamp
		nt.TZ = strings.Contains(s, "TZ") || strings.Contains(s, "WITH TIME ZONE")
	case strings.HasPrefix(s, "TIME"): // after TIMESTAMP
		nt.Fam = FamTime
		nt.TZ = strings.Contains(s, "TZ") || strings.Contains(s, "WITH TIME ZONE")
	case s == "DATE":
		nt.Fam = FamDate
	case s == "INTERVAL":
		nt.Fam = FamInterval
	case s == "UUID":
		nt.Fam = FamUUID
	case s == "JSON":
		nt.Fam = FamJSON
	case s == "BLOB" || s == "BYTEA" || s == "BINARY" || s == "VARBINARY":
		nt.Fam = FamBlob
	case isStringType(s):
		nt.Fam = FamString
	default:
		if bits, signed, ok := intType(s); ok {
			nt.Fam, nt.Bits, nt.Signed = FamInt, bits, signed
		} else if bits, ok := floatType(s); ok {
			nt.Fam, nt.Bits = FamFloat, bits
		} else {
			nt.Fam = FamUnknown // unknown/exotic: widen freely
		}
	}
	return nt
}

func isStringType(s string) bool {
	switch {
	case strings.HasPrefix(s, "VARCHAR"), strings.HasPrefix(s, "CHAR"),
		strings.HasPrefix(s, "BPCHAR"), s == "STRING", s == "TEXT", s == "ENUM":
		return true
	}
	return strings.HasPrefix(s, "ENUM(")
}

// intType maps DuckDB integer type names to (bits, signed).
func intType(s string) (bits int, signed, ok bool) {
	switch s {
	case "TINYINT", "INT1":
		return 8, true, true
	case "SMALLINT", "INT2", "SHORT":
		return 16, true, true
	case "INTEGER", "INT", "INT4", "SIGNED":
		return 32, true, true
	case "BIGINT", "INT8", "LONG":
		return 64, true, true
	case "HUGEINT", "INT128":
		return 128, true, true
	case "UTINYINT":
		return 8, false, true
	case "USMALLINT":
		return 16, false, true
	case "UINTEGER":
		return 32, false, true
	case "UBIGINT":
		return 64, false, true
	case "UHUGEINT":
		return 128, false, true
	}
	return 0, false, false
}

func floatType(s string) (bits int, ok bool) {
	switch s {
	case "FLOAT", "REAL", "FLOAT4":
		return 32, true
	case "DOUBLE", "FLOAT8":
		return 64, true
	}
	return 0, false
}

// parseDecimal extracts precision and scale from DECIMAL(p,s); defaults to
// DuckDB's (18,3) when absent.
func parseDecimal(s string) (prec, scale int) {
	prec, scale = 18, 3
	open := strings.IndexByte(s, '(')
	close := strings.IndexByte(s, ')')
	if open < 0 || close < open {
		return prec, scale
	}
	parts := strings.Split(s[open+1:close], ",")
	if len(parts) >= 1 {
		if p, ok := atoi(strings.TrimSpace(parts[0])); ok {
			prec = p
		}
	}
	if len(parts) >= 2 {
		if sc, ok := atoi(strings.TrimSpace(parts[1])); ok {
			scale = sc
		}
	}
	return prec, scale
}

// parseStructFields parses "STRUCT(name TYPE, other TYPE2)" into ordered fields.
// It splits on top-level commas only (nested STRUCT/MAP/parentheses are kept
// intact) and separates each field's name from its type at the first top-level
// space.
func parseStructFields(raw string) []Field {
	inner, ok := parens(raw)
	if !ok {
		return nil
	}
	var fields []Field
	for _, part := range splitTopLevel(inner, ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, typ := splitFieldNameType(part)
		if name == "" {
			continue
		}
		fields = append(fields, Field{Name: name, Type: Normalize(typ)})
	}
	return fields
}

// parseMapKV parses "MAP(KEYTYPE, VALTYPE)".
func parseMapKV(raw string) (key, val NormType) {
	inner, ok := parens(raw)
	if !ok {
		return NormType{Fam: FamUnknown}, NormType{Fam: FamUnknown}
	}
	parts := splitTopLevel(inner, ',')
	if len(parts) >= 1 {
		key = Normalize(strings.TrimSpace(parts[0]))
	}
	if len(parts) >= 2 {
		val = Normalize(strings.TrimSpace(parts[1]))
	}
	return key, val
}

// splitFieldNameType splits a struct field "name TYPE" at the first top-level
// space. A quoted "name" is unquoted. Everything after is the type.
func splitFieldNameType(part string) (name, typ string) {
	if strings.HasPrefix(part, `"`) {
		if end := strings.IndexByte(part[1:], '"'); end >= 0 {
			name = part[1 : 1+end]
			typ = strings.TrimSpace(part[2+end:])
			return name, typ
		}
	}
	depth := 0
	for i := 0; i < len(part); i++ {
		switch part[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ' ':
			if depth == 0 {
				return part[:i], strings.TrimSpace(part[i+1:])
			}
		}
	}
	return part, ""
}

// parens returns the substring inside the first top-level (...) pair.
func parens(s string) (string, bool) {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return "", false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// splitTopLevel splits s on sep, ignoring separators nested inside parentheses.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}
