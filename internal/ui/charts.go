package ui

import (
	"strconv"

	"github.com/apexion/apexion/internal/model"
)

func itoa(i int) string { return strconv.Itoa(i) }

// inputClass is the shared styling for text inputs/selects in forms.
const inputClass = "w-full px-3 py-1.5 rounded-lg bg-base-950 border border-base-800 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-brand-500/40"

func connEndpoint(c model.Connection) string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	if c.Provider == "aws" {
		return "s3.amazonaws.com"
	}
	return "—"
}

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

// partitionKeyList joins partition keys for display.
func partitionKeyList(keys []string) string {
	if len(keys) == 0 {
		return "—"
	}
	return join(keys, " / ")
}

// allFormats is the selectable format list for filters.
var allFormats = []model.Format{
	model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL,
	model.FormatParquet, model.FormatAvro, model.FormatORC,
	model.FormatIceberg, model.FormatDelta,
}
