// Package virtualpath turns a physical object key into a virtual Hive-partitioned
// path, so a store that lays data out as bare directories
// (2023-10-11/US/IDFA/file.parquet) can be treated as if it were Hive
// partitioned (pt0=2023-10-11/pt1=US/pt2=IDFA/file.parquet) — without touching
// the stored objects.
//
// It is a pure, self-contained utility: no I/O, no DuckDB, no storage SDK, no
// regular expressions. Only path/string utilities. Partitions are modelled as
// (Name, Value) pairs so callers can move from positional names (pt0, pt1) to
// semantic names (date, country) later without changing the storage model.
package virtualpath

import "strings"

// Partition is one directory level exposed as a column.
type Partition struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// VirtualPath is the result of virtualizing an object key.
type VirtualPath struct {
	Original   string      `json:"original"` // normalized physical key (no leading slash)
	Virtual    string      `json:"virtual"`  // Hive-style path (pt0=…/pt1=…/file)
	Filename   string      `json:"filename"` // last path segment (never a partition)
	Partitions []Partition `json:"partitions"`
}

// Builder generates virtual paths using a configurable partition-column naming
// scheme: Prefix + Separator + index, e.g. "pt"+""+0 = "pt0", "pt"+"_"+0 = "pt_0".
type Builder struct {
	prefix    string
	separator string
}

// New returns a Builder. Empty prefix defaults to "pt"; separator defaults to
// "" (so names are pt0, pt1, …).
func New(prefix, separator string) Builder {
	if prefix == "" {
		prefix = "pt"
	}
	return Builder{prefix: prefix, separator: separator}
}

// name returns the partition column name for a zero-based index.
func (b Builder) name(i int) string {
	return b.prefix + b.separator + itoa(i)
}

// Build virtualizes objectKey, converting the first partitionDepth parent
// directories into partitions (clamped to the number of directories present).
// The filename (everything after the last slash) is never partitioned.
//
// The key is normalized first: backslashes → '/', a leading slash is dropped,
// and empty directory segments (from '//') are collapsed. UTF-8, spaces, dots
// and '=' inside names are preserved verbatim.
func (b Builder) Build(objectKey string, partitionDepth int) VirtualPath {
	dirs, filename, normalized := split(objectKey)

	k := partitionDepth
	if k < 0 {
		k = 0
	}
	if k > len(dirs) {
		k = len(dirs)
	}

	parts := make([]Partition, 0, k)
	segs := make([]string, 0, len(dirs)+1)
	for i, d := range dirs {
		if i < k {
			parts = append(parts, Partition{Name: b.name(i), Value: d})
			segs = append(segs, b.name(i)+"="+d)
		} else {
			segs = append(segs, d) // beyond partition depth: keep literal
		}
	}
	segs = append(segs, filename)

	return VirtualPath{
		Original:   normalized,
		Virtual:    strings.Join(segs, "/"),
		Filename:   filename,
		Partitions: parts,
	}
}

// BuildVirtualPath virtualizes objectKey with the default naming scheme
// (pt0, pt1, …).
func BuildVirtualPath(objectKey string, partitionDepth int) VirtualPath {
	return New("", "").Build(objectKey, partitionDepth)
}

// Translate reverses a virtual path to its physical object key by stripping the
// "<prefix><i>=" from each partition segment (e.g. pt0=2023/pt1=US/file →
// 2023/US/file). It is a pure path alias — no object is copied or moved — so the
// same bytes are still read from the original storage location.
func (b Builder) Translate(virtual string) string {
	parts := strings.Split(virtual, "/")
	for i, seg := range parts {
		if !strings.HasPrefix(seg, b.prefix) {
			continue
		}
		if eq := strings.IndexByte(seg, '='); eq > 0 {
			parts[i] = seg[eq+1:]
		}
	}
	return strings.Join(parts, "/")
}

// split normalizes a key and returns its directory segments (empty ones
// collapsed), the filename (last segment, possibly empty for a trailing slash),
// and the normalized key.
func split(objectKey string) (dirs []string, filename, normalized string) {
	normalized = strings.ReplaceAll(objectKey, "\\", "/")
	normalized = strings.TrimLeft(normalized, "/")

	parts := strings.Split(normalized, "/")
	filename = parts[len(parts)-1]
	for _, d := range parts[:len(parts)-1] {
		if d != "" { // collapse "//"
			dirs = append(dirs, d)
		}
	}
	return dirs, filename, normalized
}

// itoa converts a small non-negative int to a decimal string without importing
// strconv (keeps the package minimal). Handles arbitrary depth.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
