package compat

import "strings"

// Options tunes the balanced default. BoolIntCoerce, when true, treats
// boolean and tinyint as compatible (the spec's "configurable" case); the
// balanced default leaves it false.
type Options struct {
	BoolIntCoerce bool
}

// Result is the outcome of comparing two normalized types.
//   - OK=false: the types cannot share a column (✕).
//   - OK=true, Widened=false: a safe promotion, shown as compatible (✓)
//     (int32+int64, varchar+string).
//   - OK=true, Widened=true: compatible but semantically widened, shown as a
//     warning (⚠) (date+timestamp, int+float).
type Result struct {
	OK      bool
	Widened bool
	To      NormType
	Reason  string
}

func incompat(a, b NormType) Result {
	return Result{Reason: a.String() + " vs " + b.String()}
}

// Compatible reports whether two columns of types a and b can be read together,
// returning the merged (widened) type. Rules implement the "balanced" policy:
// numeric widening within/across integer and float families, varchar/string
// equivalence, date/timestamp widening, and structural recursion for
// list/struct/map. Unknown (all-null) columns cast to anything.
func Compatible(a, b NormType, opts Options) Result {
	// All-null columns adopt the other side's type for free.
	if a.Fam == FamUnknown {
		return Result{OK: true, To: b}
	}
	if b.Fam == FamUnknown {
		return Result{OK: true, To: a}
	}

	// bool + tinyint: configurable; incompatible under the balanced default.
	if opts.BoolIntCoerce {
		if a.Fam == FamBool && b.Fam == FamInt && b.Bits == 8 {
			return Result{OK: true, Widened: true, To: b}
		}
		if b.Fam == FamBool && a.Fam == FamInt && a.Bits == 8 {
			return Result{OK: true, Widened: true, To: a}
		}
	}

	if a.Fam == b.Fam {
		return sameFamily(a, b, opts)
	}
	return crossFamily(a, b)
}

func sameFamily(a, b NormType, opts Options) Result {
	switch a.Fam {
	case FamBool, FamDate, FamInterval, FamUUID, FamBlob, FamJSON:
		return Result{OK: true, To: a} // no sub-attributes to widen
	case FamString:
		return Result{OK: true, To: NormType{Fam: FamString, Raw: "VARCHAR"}}
	case FamInt:
		signed := a.Signed || b.Signed
		bits := max(a.Bits, b.Bits)
		// Merging a signed with an equally-wide-or-wider unsigned needs one more
		// size class of headroom to hold the unsigned range as signed.
		if a.Signed != b.Signed {
			bits = nextIntSize(bits)
		}
		return Result{OK: true, To: intOf(bits, signed)}
	case FamFloat:
		return Result{OK: true, To: floatOf(max(a.Bits, b.Bits))}
	case FamDecimal:
		to := NormType{Fam: FamDecimal, Prec: max(a.Prec, b.Prec), Scale: max(a.Scale, b.Scale)}
		return Result{OK: true, Widened: a.Prec != b.Prec || a.Scale != b.Scale, To: to}
	case FamTime:
		return Result{OK: true, Widened: a.TZ != b.TZ, To: NormType{Fam: FamTime, TZ: a.TZ || b.TZ}}
	case FamTimestamp:
		return Result{OK: true, Widened: a.TZ != b.TZ, To: NormType{Fam: FamTimestamp, TZ: a.TZ || b.TZ}}
	case FamList:
		inner := Compatible(deref(a.Elem), deref(b.Elem), opts)
		if !inner.OK {
			return incompat(a, b)
		}
		el := inner.To
		return Result{OK: true, Widened: inner.Widened, To: NormType{Fam: FamList, Elem: &el}}
	case FamMap:
		k := Compatible(deref(a.Key), deref(b.Key), opts)
		v := Compatible(deref(a.Val), deref(b.Val), opts)
		if !k.OK || !v.OK {
			return incompat(a, b)
		}
		kt, vt := k.To, v.To
		return Result{OK: true, Widened: k.Widened || v.Widened, To: NormType{Fam: FamMap, Key: &kt, Val: &vt}}
	case FamStruct:
		if len(a.Fields) != len(b.Fields) {
			return incompat(a, b)
		}
		merged := make([]Field, len(a.Fields))
		widened := false
		for i := range a.Fields {
			if !strings.EqualFold(a.Fields[i].Name, b.Fields[i].Name) {
				return incompat(a, b)
			}
			r := Compatible(a.Fields[i].Type, b.Fields[i].Type, opts)
			if !r.OK {
				return incompat(a, b)
			}
			widened = widened || r.Widened
			merged[i] = Field{Name: a.Fields[i].Name, Type: r.To}
		}
		return Result{OK: true, Widened: widened, To: NormType{Fam: FamStruct, Fields: merged}}
	}
	return Result{OK: true, To: a}
}

// crossFamily handles the allowed widenings between different families.
func crossFamily(a, b NormType) Result {
	fa, fb := a.Fam, b.Fam
	// Numeric tower: Int < Float < Decimal-ish. Any int+float → double; int or
	// float + decimal → decimal.
	numeric := func(f Family) bool { return f == FamInt || f == FamFloat || f == FamDecimal }
	if numeric(fa) && numeric(fb) {
		switch {
		case fa == FamDecimal || fb == FamDecimal:
			d := a
			if fb == FamDecimal {
				d = b
			}
			return Result{OK: true, Widened: true, To: NormType{Fam: FamDecimal, Prec: max(d.Prec, 18), Scale: d.Scale}}
		default: // int + float
			return Result{OK: true, Widened: true, To: floatOf(64)}
		}
	}
	// Date and Timestamp widen to Timestamp.
	if (fa == FamDate && fb == FamTimestamp) || (fa == FamTimestamp && fb == FamDate) {
		tz := (fa == FamTimestamp && a.TZ) || (fb == FamTimestamp && b.TZ)
		return Result{OK: true, Widened: true, To: NormType{Fam: FamTimestamp, TZ: tz}}
	}
	// JSON and String are interchangeable in DuckDB (JSON is a VARCHAR alias).
	if (fa == FamJSON && fb == FamString) || (fa == FamString && fb == FamJSON) {
		return Result{OK: true, Widened: true, To: NormType{Fam: FamString, Raw: "VARCHAR"}}
	}
	return incompat(a, b)
}

func deref(n *NormType) NormType {
	if n == nil {
		return NormType{Fam: FamUnknown}
	}
	return *n
}

func intOf(bits int, signed bool) NormType { return NormType{Fam: FamInt, Bits: bits, Signed: signed} }
func floatOf(bits int) NormType            { return NormType{Fam: FamFloat, Bits: bits} }

// nextIntSize bumps to the next standard integer width (capped at 128).
func nextIntSize(bits int) int {
	switch {
	case bits <= 8:
		return 16
	case bits <= 16:
		return 32
	case bits <= 32:
		return 64
	default:
		return 128
	}
}
