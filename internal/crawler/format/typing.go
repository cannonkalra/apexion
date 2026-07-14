package format

import (
	"strconv"
	"strings"
	"time"

	"github.com/apexion/apexion/internal/model"
)

// nullTokens are string values treated as null when inferring column types.
var nullTokens = map[string]bool{
	"": true, "null": true, "NULL": true, "Null": true, "na": true, "NA": true,
	"n/a": true, "N/A": true, "nil": true, "none": true, "None": true, "\\N": true,
}

// IsNull reports whether a raw cell value represents a null.
func IsNull(s string) bool { return nullTokens[strings.TrimSpace(s)] }

// DateLayouts are the timestamp/date formats the inferrer recognizes, most
// specific first. The matching layout is returned by DetectDateLayout.
var DateLayouts = []struct {
	Layout string
	Type   model.DataType
}{
	{time.RFC3339Nano, model.TypeTimestamp},
	{time.RFC3339, model.TypeTimestamp},
	{"2006-01-02T15:04:05", model.TypeTimestamp},
	{"2006-01-02 15:04:05.999999", model.TypeTimestamp},
	{"2006-01-02 15:04:05", model.TypeTimestamp},
	{"2006/01/02 15:04:05", model.TypeTimestamp},
	{"01/02/2006 15:04:05", model.TypeTimestamp},
	{"2006-01-02", model.TypeDate},
	{"2006/01/02", model.TypeDate},
	{"01/02/2006", model.TypeDate},
	{"02-01-2006", model.TypeDate},
	{"15:04:05", model.TypeTime},
}

// DetectDateLayout returns the matching layout and logical type for a date-like
// value, or ("", TypeUnknown) if none matches.
func DetectDateLayout(s string) (string, model.DataType) {
	s = strings.TrimSpace(s)
	if len(s) < 6 || len(s) > 40 {
		return "", model.TypeUnknown
	}
	for _, dl := range DateLayouts {
		if _, err := time.Parse(dl.Layout, s); err == nil {
			return dl.Layout, dl.Type
		}
	}
	return "", model.TypeUnknown
}

// InferCellType classifies a single raw string value into a logical type.
func InferCellType(s string) model.DataType {
	t := strings.TrimSpace(s)
	if IsNull(t) {
		return model.TypeNull
	}
	// Boolean.
	switch strings.ToLower(t) {
	case "true", "false", "t", "f", "yes", "no", "y", "n":
		return model.TypeBoolean
	}
	// Integer.
	if _, err := strconv.ParseInt(t, 10, 64); err == nil {
		return model.TypeInteger
	}
	// Float / decimal.
	if _, err := strconv.ParseFloat(t, 64); err == nil {
		if strings.ContainsAny(t, "eE") || decimalPlaces(t) <= 6 {
			return model.TypeFloat
		}
		return model.TypeDecimal
	}
	// UUID.
	if isUUID(t) {
		return model.TypeUUID
	}
	// Date / timestamp.
	if _, dt := DetectDateLayout(t); dt != model.TypeUnknown {
		return dt
	}
	// JSON object/array.
	if (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) ||
		(strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
		return model.TypeJSON
	}
	return model.TypeString
}

func decimalPlaces(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

// MergeTypes reconciles two observed logical types into a common type, used to
// widen a column's type across sample rows. Null is absorbed by any type.
func MergeTypes(a, b model.DataType) model.DataType {
	if a == b {
		return a
	}
	if a == model.TypeNull || a == model.TypeUnknown {
		return b
	}
	if b == model.TypeNull || b == model.TypeUnknown {
		return a
	}
	// Numeric widening: integer < decimal < float.
	numeric := map[model.DataType]int{model.TypeInteger: 1, model.TypeDecimal: 2, model.TypeFloat: 3}
	if ra, oka := numeric[a]; oka {
		if rb, okb := numeric[b]; okb {
			if ra > rb {
				return a
			}
			return b
		}
	}
	// Date + timestamp -> timestamp.
	if (a == model.TypeDate && b == model.TypeTimestamp) || (a == model.TypeTimestamp && b == model.TypeDate) {
		return model.TypeTimestamp
	}
	// Anything else collapses to string.
	return model.TypeString
}

// InferColumnType infers a column's type from a set of sample cell values.
// It returns the widened type and whether any null was observed.
func InferColumnType(values []string) (model.DataType, bool) {
	current := model.TypeUnknown
	nullable := false
	for _, v := range values {
		if IsNull(v) {
			nullable = true
			continue
		}
		current = MergeTypes(current, InferCellType(v))
	}
	if current == model.TypeUnknown {
		current = model.TypeString
		nullable = true
	}
	return current, nullable
}
