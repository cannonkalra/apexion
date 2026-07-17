package dataviewer

import (
	"fmt"
	"strconv"
	"strings"
)

// projection is one SELECT expression in the batched scalar-stats query (Query A)
// paired with how to fold its (stringified) result into a ColumnProfile. col is
// the profile index it targets, or -1 for the single global count(*).
type projection struct {
	expr string
	col  int
	set  func(p *ColumnProfile, raw string)
}

// buildStatsSQL builds the single batched query that computes every column's
// scalar statistics in one round-trip. Every value is cast to ::VARCHAR so the
// engine's existing stringifying scan is sufficient — no typed value path. The
// reader is wrapped in a sample CTE bounded by cap.
func buildStatsSQL(profiles []ColumnProfile, from string, cap int) (string, []projection) {
	plan := make([]projection, 0, 1+len(profiles)*4)
	plan = append(plan, projection{expr: "count(*)", col: -1})

	for i := range profiles {
		q := quoteIdent(profiles[i].Name)
		plan = append(plan,
			projection{expr: "count(" + q + ")", col: i, set: setNonNull},
			projection{expr: "approx_count_distinct(" + q + ")", col: i, set: setDistinct},
			projection{expr: "min(" + q + ")::VARCHAR", col: i, set: setMin},
			projection{expr: "max(" + q + ")::VARCHAR", col: i, set: setMax},
		)
		switch {
		case profiles[i].Kind.isNumeric():
			d := "TRY_CAST(" + q + " AS DOUBLE)"
			plan = append(plan,
				projection{expr: "avg(" + d + ")::VARCHAR", col: i, set: setMean},
				projection{expr: "stddev_pop(" + d + ")::VARCHAR", col: i, set: setStd},
				projection{expr: "quantile_cont(" + d + ", 0.5)::VARCHAR", col: i, set: setMedian},
			)
		case profiles[i].Kind.isString():
			l := "length(" + q + ")"
			plan = append(plan,
				projection{expr: "min(" + l + ")::VARCHAR", col: i, set: setMinLen},
				projection{expr: "max(" + l + ")::VARCHAR", col: i, set: setMaxLen},
				projection{expr: "avg(" + l + ")::VARCHAR", col: i, set: setAvgLen},
			)
		}
	}

	exprs := make([]string, len(plan))
	for k, pj := range plan {
		exprs[k] = fmt.Sprintf("%s AS f%d", pj.expr, k)
	}
	sql := fmt.Sprintf(
		"WITH t AS (SELECT * FROM %s LIMIT %d) SELECT %s FROM t",
		from, cap, strings.Join(exprs, ", "),
	)
	return sql, plan
}

// applyStatsRow folds the single result row (positional, aligned to plan) into
// the profiles. A short/missing row leaves the affected fields at zero.
func applyStatsRow(row []string, plan []projection, profiles []ColumnProfile) {
	for k, pj := range plan {
		if k >= len(row) {
			return
		}
		raw := row[k]
		if pj.col < 0 { // global count(*)
			n := atoi64(raw)
			for i := range profiles {
				profiles[i].Count = n
			}
			continue
		}
		if pj.set != nil {
			pj.set(&profiles[pj.col], raw)
		}
	}
}

func setNonNull(p *ColumnProfile, raw string)  { p.NonNull = atoi64(raw) }
func setDistinct(p *ColumnProfile, raw string) { p.Distinct = atoi64(raw) }
func setMin(p *ColumnProfile, raw string)      { p.Min = raw }
func setMax(p *ColumnProfile, raw string)      { p.Max = raw }
func setMean(p *ColumnProfile, raw string)     { p.Mean = atof(raw) }
func setStd(p *ColumnProfile, raw string)      { p.Std = atof(raw) }
func setMedian(p *ColumnProfile, raw string)   { p.Median = atof(raw) }
func setMinLen(p *ColumnProfile, raw string)   { p.MinLen = atoi64(raw) }
func setMaxLen(p *ColumnProfile, raw string)   { p.MaxLen = atoi64(raw) }
func setAvgLen(p *ColumnProfile, raw string)   { p.AvgLen = atof(raw) }

func atoi64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// avg(length()) etc. can arrive as a float string even when integral.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

func atof(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
