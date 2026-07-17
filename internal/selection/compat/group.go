package compat

import "strings"

// FileSchema is one file's discovered column list.
type FileSchema struct {
	Ref  FileRef
	Cols []Column
}

// MergedColumn is a column in the combined virtual-table schema.
type MergedColumn struct {
	Name    string
	Type    NormType
	Widened bool // any member widened this column (⚠)
}

// ReasonKind classifies why a file was rejected or flagged.
type ReasonKind string

const (
	KindShape  ReasonKind = "shape"  // column count/name/order mismatch
	KindType   ReasonKind = "type"   // incompatible column type
	KindWiden  ReasonKind = "widen"  // compatible but widened (⚠, still a member)
	KindFormat ReasonKind = "format" // different file format (mixed-format split)
)

// Reason explains one file/column status for the UI's error and detail views.
type Reason struct {
	File     FileRef    `json:"file"`
	Column   string     `json:"column"`
	Expected string     `json:"expected"`
	Actual   string     `json:"actual"`
	Kind     ReasonKind `json:"kind"`
}

// Grouping is the compatibility verdict for a set of files sharing one format:
// the members that combine into one virtual table (with the merged schema and
// any widening warnings) plus the rejected files with reasons.
type Grouping struct {
	Anchor   FileSchema     // the file whose shape defines the group
	Members  []FileRef      // files that combine (includes Anchor)
	Merged   []MergedColumn // combined schema
	Warnings []Reason       // widened columns among members (Kind=widen)
	Rejected []Reason       // files that don't fit, with reasons (Kind=shape|type)
}

// signature is the order-sensitive join of lowercased column names — two files
// can only combine if their signatures match (same count, names, order).
func signature(cols []Column) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = strings.ToLower(c.Name)
	}
	return strings.Join(names, "\x00")
}

// Group partitions files into the largest mutually-compatible set. Files join
// iff they share the anchor's shape (same column count + names + order) and
// every column type is Compatible with the running merged type. The anchor is
// the MODAL shape (the largest bucket of files sharing a signature), so a single
// odd file cannot reject the majority. Files not matching the anchor shape, or
// with an incompatible column type, are Rejected with a reason.
//
// Input files are assumed to share one format; callers split by format first
// (see the selection service). Group is pure and deterministic.
func Group(files []FileSchema, opts Options) Grouping {
	if len(files) == 0 {
		return Grouping{}
	}

	// Pick the modal signature (ties broken by first appearance for determinism).
	counts := map[string]int{}
	var order []string
	for _, f := range files {
		sig := signature(f.Cols)
		if counts[sig] == 0 {
			order = append(order, sig)
		}
		counts[sig]++
	}
	best := order[0]
	for _, sig := range order {
		if counts[sig] > counts[best] {
			best = sig
		}
	}

	// Anchor = first file with the modal signature.
	var g Grouping
	for _, f := range files {
		if signature(f.Cols) == best {
			g.Anchor = f
			break
		}
	}

	// Seed the merged schema from the anchor.
	g.Merged = make([]MergedColumn, len(g.Anchor.Cols))
	for i, c := range g.Anchor.Cols {
		g.Merged[i] = MergedColumn{Name: c.Name, Type: c.Type}
	}

	// Fold every file into the group or reject it.
	for _, f := range files {
		if signature(f.Cols) != best {
			g.Rejected = append(g.Rejected, shapeReason(f, g.Anchor))
			continue
		}
		// Try to fold each column; collect any incompatibility before committing.
		folded := make([]MergedColumn, len(g.Merged))
		copy(folded, g.Merged)
		var typeReasons []Reason
		widenReasons := map[int]Reason{}
		for i := range f.Cols {
			r := Compatible(folded[i].Type, f.Cols[i].Type, opts)
			if !r.OK {
				typeReasons = append(typeReasons, Reason{
					File: f.Ref, Column: g.Anchor.Cols[i].Name, Kind: KindType,
					Expected: folded[i].Type.String(), Actual: f.Cols[i].Type.String(),
				})
				continue
			}
			folded[i].Type = r.To
			if r.Widened {
				folded[i].Widened = true
				widenReasons[i] = Reason{
					File: f.Ref, Column: g.Anchor.Cols[i].Name, Kind: KindWiden,
					Expected: folded[i].Type.String(), Actual: f.Cols[i].Type.String(),
				}
			}
		}
		if len(typeReasons) > 0 {
			g.Rejected = append(g.Rejected, typeReasons...)
			continue
		}
		// Commit the fold: the file is a member.
		g.Merged = folded
		g.Members = append(g.Members, f.Ref)
		for _, wr := range widenReasons {
			g.Warnings = append(g.Warnings, wr)
		}
	}
	return g
}

// shapeReason describes how a file's columns differ from the anchor shape.
func shapeReason(f, anchor FileSchema) Reason {
	switch {
	case len(f.Cols) != len(anchor.Cols):
		return Reason{
			File: f.Ref, Kind: KindShape, Column: "(columns)",
			Expected: itoa(len(anchor.Cols)) + " columns",
			Actual:   itoa(len(f.Cols)) + " columns",
		}
	default:
		// Same count, so a name/order mismatch — report the first differing one.
		for i := range f.Cols {
			if !strings.EqualFold(f.Cols[i].Name, anchor.Cols[i].Name) {
				return Reason{
					File: f.Ref, Kind: KindShape, Column: anchor.Cols[i].Name,
					Expected: anchor.Cols[i].Name, Actual: f.Cols[i].Name,
				}
			}
		}
		return Reason{File: f.Ref, Kind: KindShape, Column: "(order)", Expected: "anchor order", Actual: "different order"}
	}
}
