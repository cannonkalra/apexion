package crawler

import (
	"path"
	"strings"

	"github.com/apexion/apexion/internal/catalog/discovery"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// grouping.go turns a flat stream of object keys into logical datasets. The
// classification itself (dataset root + partition columns) lives in package
// discovery; this file only adapts its result to the crawler's aggregation and
// keeps the table-format / ignore heuristics.

// partitionInfo captures the partition columns/values for one key.
type partitionInfo struct {
	root     string            // dataset root prefix (no trailing slash)
	strategy string            // hive | positional | legacy
	keys     []string          // ordered partition column names
	values   map[string]string // column -> value for this object
}

// deriveDataset classifies a data key into a dataset root and its partitions
// using the configured discovery strategy. crawlRoot is the prefix the crawl
// started at (the dataset root for positional layouts).
func deriveDataset(key, crawlRoot string, disp discovery.Dispatcher) partitionInfo {
	c := disp.Classify(key, crawlRoot)
	info := partitionInfo{root: c.Root, strategy: c.Strategy, values: map[string]string{}}
	for _, p := range c.Partitions {
		info.keys = append(info.keys, p.Name)
		info.values[p.Name] = p.Value
	}
	return info
}

// tableMarker recognizes table-format metadata keys and returns the table root
// and format, or ok=false for ordinary data keys.
func tableMarker(key string) (root string, f model.Format, ok bool) {
	// Delta Lake: <root>/_delta_log/*
	if idx := strings.Index(key, "/_delta_log/"); idx >= 0 {
		return key[:idx], model.FormatDelta, true
	}
	if strings.HasPrefix(key, "_delta_log/") {
		return "", model.FormatDelta, true
	}
	// Iceberg: <root>/metadata/*.metadata.json or manifest .avro
	if idx := strings.Index(key, "/metadata/"); idx >= 0 {
		base := path.Base(key)
		if strings.HasSuffix(base, ".metadata.json") || strings.HasSuffix(base, ".avro") ||
			strings.HasPrefix(base, "snap-") || base == "version-hint.text" {
			return key[:idx], model.FormatIceberg, true
		}
	}
	if strings.HasPrefix(key, "metadata/") && strings.HasSuffix(key, ".metadata.json") {
		return "", model.FormatIceberg, true
	}
	return "", model.FormatUnknown, false
}

// datasetName derives a human-friendly dataset name from a root prefix.
func datasetName(bucket, root string) string {
	if root == "" {
		return bucket
	}
	return path.Base(root)
}

// shouldIgnore reports whether a key is a hidden/marker file to skip.
func shouldIgnore(key string, ignoreHidden bool) bool {
	if !ignoreHidden {
		return false
	}
	base := path.Base(key)
	if base == "" || strings.HasSuffix(key, "/") {
		return true
	}
	if strings.HasPrefix(base, ".") {
		return true
	}
	// Spark success/marker files inside data directories.
	switch base {
	case "_SUCCESS", "_metadata", "_common_metadata", "_started", "_committed":
		return true
	}
	if strings.HasPrefix(base, "_temporary") || strings.Contains(key, "/_temporary/") {
		return true
	}
	return false
}

// dominantFormat returns the format with the most votes (ties broken by a fixed
// preference order that favors self-describing formats).
func dominantFormat(votes map[model.Format]int) model.Format {
	best := model.FormatUnknown
	bestN := -1
	for _, f := range formatPreference {
		if n := votes[f]; n > bestN {
			best, bestN = f, n
		}
	}
	return best
}

var formatPreference = []model.Format{
	model.FormatParquet, model.FormatAvro, model.FormatORC,
	model.FormatJSONL, model.FormatJSON, model.FormatCSV, model.FormatTSV,
	model.FormatUnknown,
}

var _ = format.IsHidden
