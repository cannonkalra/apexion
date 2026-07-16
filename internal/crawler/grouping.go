package crawler

import (
	"path"
	"strings"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// grouping.go turns a flat stream of object keys into logical datasets, the way
// AWS Glue's crawler does: it collapses folders of same-format files, recognizes
// Hive-style k=v partition folders, and detects table-format roots.

// partitionInfo captures the partition columns/values parsed from a key.
type partitionInfo struct {
	root   string            // dataset root prefix (no trailing slash)
	keys   []string          // ordered partition column names
	values map[string]string // column -> value for this object
}

// isKV reports whether a path segment is a Hive partition segment (k=v).
func isKV(seg string) (key, val string, ok bool) {
	i := strings.IndexByte(seg, '=')
	if i <= 0 || i == len(seg)-1 {
		return "", "", false
	}
	return seg[:i], seg[i+1:], true
}

// deriveDataset computes the dataset root and partition values for a data key.
func deriveDataset(key string) partitionInfo {
	dir := path.Dir(key)
	if dir == "." {
		dir = ""
	}
	segs := splitNonEmpty(dir, "/")

	// Trailing contiguous k=v segments are partitions.
	firstPart := len(segs)
	for i := 0; i < len(segs); i++ {
		if _, _, ok := isKV(segs[i]); ok {
			firstPart = i
			break
		}
	}
	rootSegs := segs[:firstPart]
	partSegs := segs[firstPart:]

	info := partitionInfo{
		root:   strings.Join(rootSegs, "/"),
		values: map[string]string{},
	}
	for _, ps := range partSegs {
		if k, v, ok := isKV(ps); ok {
			info.keys = append(info.keys, k)
			info.values[k] = v
		}
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

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
