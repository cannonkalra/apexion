package ui

import (
	"sort"
	"strconv"

	"github.com/apexion/apexion/internal/model"
)

func itoa(i int) string { return strconv.Itoa(i) }

func typeAt(types []string, i int) string {
	if i >= 0 && i < len(types) {
		return types[i]
	}
	return ""
}

func cellOrNull(s string) string {
	if s == "" {
		return "∅"
	}
	return s
}

// formatRow is a single bar in the dashboard format-distribution chart.
type formatRow struct {
	Label string
	Count int64
	Pct   float64
	Color string
}

var formatBarColor = map[string]string{
	"parquet": "bg-brand-500",
	"csv":     "bg-accent-emerald",
	"tsv":     "bg-accent-emerald/70",
	"json":    "bg-accent-amber",
	"jsonl":   "bg-accent-amber/70",
	"avro":    "bg-accent-violet",
	"orc":     "bg-accent-violet/70",
	"iceberg": "bg-accent-rose",
	"delta":   "bg-accent-rose/70",
	"unknown": "bg-base-600",
}

// formatRows sorts and scales the format breakdown for rendering.
func formatRows(formats map[string]int64) []formatRow {
	var max int64 = 1
	for _, c := range formats {
		if c > max {
			max = c
		}
	}
	rows := make([]formatRow, 0, len(formats))
	for k, c := range formats {
		color := formatBarColor[k]
		if color == "" {
			color = "bg-base-600"
		}
		rows = append(rows, formatRow{
			Label: k, Count: c, Pct: float64(c) / float64(max) * 100, Color: color,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	return rows
}

// partitionSummary joins partition keys for display.
func partitionKeyList(keys []string) string {
	if len(keys) == 0 {
		return "—"
	}
	return join(keys, " / ")
}

func lineageKindColor(kind string) string {
	switch kind {
	case "bucket":
		return "bg-brand-400"
	case "dataset":
		return "bg-accent-emerald"
	case "table":
		return "bg-accent-amber"
	case "column":
		return "bg-accent-violet"
	case "partition":
		return "bg-accent-rose"
	default:
		return "bg-base-500"
	}
}

func lineageKindBorder(kind string) string {
	switch kind {
	case "bucket":
		return "border-brand-500/40 bg-brand-500/5"
	case "dataset":
		return "border-accent-emerald/40 bg-accent-emerald/5"
	case "table":
		return "border-accent-amber/40 bg-accent-amber/5"
	case "column":
		return "border-accent-violet/40 bg-accent-violet/5"
	case "partition":
		return "border-accent-rose/40 bg-accent-rose/5"
	default:
		return "border-base-700 bg-base-850"
	}
}

func lineageNodeHead(nodes []model.LineageNode, n int) []model.LineageNode {
	if len(nodes) > n {
		return nodes[:n]
	}
	return nodes
}

// allFormats is the selectable format list for filters.
var allFormats = []model.Format{
	model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL,
	model.FormatParquet, model.FormatAvro, model.FormatORC,
	model.FormatIceberg, model.FormatDelta,
}
