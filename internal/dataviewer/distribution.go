package dataviewer

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/apexion/apexion/internal/duckdb"
)

// topN is how many categorical values to surface per column.
const topN = 8

// numericHistBins is the bucket count for numeric mini-distributions.
const numericHistBins = 12

// buildTopNSQL builds one batched UNION ALL that returns the top-N most frequent
// values for every non-numeric column, tagged by column index. Returns ""/false
// when there are no categorical columns to profile. Each branch is parenthesised
// so its per-branch ORDER BY / LIMIT is valid inside the union.
func buildTopNSQL(profiles []ColumnProfile, from string, cap int) (string, bool) {
	branches := make([]string, 0, len(profiles))
	for i := range profiles {
		if profiles[i].Kind.isNumeric() {
			continue // numeric -> Go-side histogram
		}
		q := quoteIdent(profiles[i].Name)
		branches = append(branches, fmt.Sprintf(
			"(SELECT %d AS col, CAST(%s AS VARCHAR) AS val, count(*)::BIGINT AS cnt "+
				"FROM t WHERE %s IS NOT NULL GROUP BY %s ORDER BY cnt DESC LIMIT %d)",
			i, q, q, q, topN,
		))
	}
	if len(branches) == 0 {
		return "", false
	}
	sql := fmt.Sprintf(
		"WITH t AS (SELECT * FROM %s LIMIT %d) %s",
		from, cap, strings.Join(branches, " UNION ALL "),
	)
	return sql, true
}

// applyTopN folds the top-N result (col, val, cnt) into each profile's TopValues.
func applyTopN(res *duckdb.Result, profiles []ColumnProfile) {
	for _, row := range res.Rows {
		if len(row) < 3 {
			continue
		}
		col, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil || col < 0 || col >= len(profiles) {
			continue
		}
		profiles[col].TopValues = append(profiles[col].TopValues, ValueCount{
			Value: row[1],
			Count: atoi64(row[2]),
		})
	}
}

// buildHistograms bins the numeric columns' preview-sample values into an
// equi-width histogram. This is the one place we touch data cells in Go — over
// the already-fetched preview sample (~100 rows), never a full scan — because a
// mini bar chart only needs the visible sample's shape.
func buildHistograms(res *duckdb.Result, profiles []ColumnProfile) {
	for i := range profiles {
		if !profiles[i].Kind.isNumeric() {
			continue
		}
		vals := numericSample(res, i)
		profiles[i].Histogram = histogram(vals, numericHistBins)
	}
}

func numericSample(res *duckdb.Result, col int) []float64 {
	out := make([]float64, 0, len(res.Rows))
	for _, row := range res.Rows {
		if col >= len(row) {
			continue
		}
		s := strings.TrimSpace(row[col])
		if s == "" {
			continue
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			out = append(out, f)
		}
	}
	return out
}

// histogram bins values into equi-width buckets. Returns nil when there is not
// enough spread to draw a meaningful distribution.
func histogram(values []float64, bins int) []Bin {
	if len(values) < 2 || bins < 1 {
		return nil
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi <= lo {
		return nil // constant column: no distribution to show
	}
	width := (hi - lo) / float64(bins)
	out := make([]Bin, bins)
	for b := range out {
		out[b].Label = fmt.Sprintf("%.4g – %.4g", lo+float64(b)*width, lo+float64(b+1)*width)
	}
	for _, v := range values {
		b := int((v - lo) / width)
		if b >= bins { // hi lands in the last bucket
			b = bins - 1
		}
		if b < 0 {
			b = 0
		}
		out[b].Count++
	}
	return out
}
