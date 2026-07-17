package ui

import (
	"strconv"

	"github.com/apexion/apexion/internal/model"
)

func itoa(i int) string { return strconv.Itoa(i) }

func itoa64(i int64) string { return strconv.FormatInt(i, 10) }

// Text inputs/selects use the shared `.input`/`.select` CSS classes (see
// assets/css/input.css) — no Go-side style constant.

func connEndpoint(c model.Connection) string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	if c.Provider == "aws" {
		return "s3.amazonaws.com"
	}
	return "—"
}

// storageLabel maps a stored provider id to a human-readable storage-type name
// for connection listings.
func storageLabel(provider string) string {
	switch provider {
	case "aws":
		return "AWS S3"
	case "minio":
		return "MinIO / S3"
	case "s3":
		return "S3-compatible"
	default:
		if provider == "" {
			return "S3-compatible"
		}
		return provider
	}
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
