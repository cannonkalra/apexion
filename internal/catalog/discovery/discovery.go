// Package discovery turns a flat stream of object keys into logical datasets:
// it finds the dataset root and the partition columns for each key. It is the
// single home for path→dataset classification — the crawler and catalog consume
// it and never parse partition structure themselves.
//
// Two strategies are supported (more can be added without touching callers):
//
//   - Hive: trailing key=value directories are partitions (date=2023/country=US).
//     The dataset root is the non-partition prefix. This is unchanged, historical
//     behaviour.
//   - Positional: bare directories below the crawl root are positional partitions
//     (2023-10-11/US/IDFA → pt0/pt1/pt2). The dataset root is the crawl root, so
//     every descendant file joins one dataset.
//
// A Dispatcher combines them: a key with key=value segments is classified Hive;
// otherwise the configured default (Positional) applies — so existing Hive
// datasets keep working while bare-directory trees become one partitioned
// dataset. It is pure and O(1) per key (no regex, no rescans).
package discovery

import (
	"path"
	"strings"

	"github.com/apexion/apexion/internal/catalog/virtualpath"
)

// Strategy names (stored on the dataset and used to pick view generation).
const (
	StrategyHive       = "hive"
	StrategyPositional = "positional"
	StrategyLegacy     = "legacy" // leaf directory = dataset, no partitions (pre-positional)
)

// Config modes accepted by New.
const (
	ModePositional = "positional" // hive-aware; non-hive → positional (default)
	ModeHive       = "hive"       // hive-aware; non-hive → legacy leaf datasets
	ModeAuto       = "auto"       // same as positional today
)

// Partition is one directory level exposed as a column.
type Partition struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Classification is the result of classifying one object key.
type Classification struct {
	Root       string      `json:"root"`     // dataset root prefix (no trailing slash)
	Strategy   string      `json:"strategy"` // hive | positional | legacy
	Partitions []Partition `json:"partitions"`
}

// Keys returns the ordered partition column names.
func (c Classification) Keys() []string {
	out := make([]string, len(c.Partitions))
	for i, p := range c.Partitions {
		out[i] = p.Name
	}
	return out
}

// Strategy classifies an object key relative to the crawl root.
type Strategy interface {
	Classify(key, crawlRoot string) Classification
	Name() string
}

// ---- Hive -----------------------------------------------------------------

// Hive treats trailing key=value directories as partitions.
type Hive struct{}

func (Hive) Name() string { return StrategyHive }

func (Hive) Classify(key, _ string) Classification {
	segs := dirSegments(key)
	first := len(segs)
	for i, s := range segs {
		if _, _, ok := splitKV(s); ok {
			first = i
			break
		}
	}
	c := Classification{Root: strings.Join(segs[:first], "/"), Strategy: StrategyHive}
	for _, s := range segs[first:] {
		if k, v, ok := splitKV(s); ok {
			c.Partitions = append(c.Partitions, Partition{Name: k, Value: v})
		}
	}
	return c
}

// ---- Positional -----------------------------------------------------------

// Positional treats every directory below the crawl root as a positional
// partition (pt0, pt1, …), so the whole tree is one dataset.
type Positional struct{ vp virtualpath.Builder }

// NewPositional builds a Positional strategy with the given column naming.
func NewPositional(prefix, separator string) Positional {
	return Positional{vp: virtualpath.New(prefix, separator)}
}

func (Positional) Name() string { return StrategyPositional }

func (p Positional) Classify(key, crawlRoot string) Classification {
	root := strings.Trim(crawlRoot, "/")
	rel := strings.TrimPrefix(strings.TrimPrefix(key, root), "/")
	vp := p.vp.Build(rel, 1<<30)
	c := Classification{Root: root, Strategy: StrategyPositional}
	for _, x := range vp.Partitions {
		c.Partitions = append(c.Partitions, Partition{Name: x.Name, Value: x.Value})
	}
	return c
}

// ---- Legacy ---------------------------------------------------------------

// Legacy is the pre-positional behaviour: the file's own directory is the
// dataset root, with no partitions.
type Legacy struct{}

func (Legacy) Name() string { return StrategyLegacy }

func (Legacy) Classify(key, _ string) Classification {
	return Classification{Root: strings.Join(dirSegments(key), "/"), Strategy: StrategyLegacy}
}

// ---- Dispatcher -----------------------------------------------------------

// Dispatcher applies Hive to key=value paths and the configured fallback to the
// rest, so Hive datasets keep working while bare trees become partitioned.
type Dispatcher struct {
	fallback Strategy
}

// New returns a Dispatcher for a config mode. prefix/separator name positional
// columns. Unknown modes behave as positional.
func New(mode, prefix, separator string) Dispatcher {
	switch mode {
	case ModeHive:
		return Dispatcher{fallback: Legacy{}}
	default: // positional / auto / anything
		return Dispatcher{fallback: NewPositional(prefix, separator)}
	}
}

func (d Dispatcher) Name() string { return "dispatcher(" + d.fallback.Name() + ")" }

// Classify routes key=value paths to Hive and everything else to the fallback.
func (d Dispatcher) Classify(key, crawlRoot string) Classification {
	if hasHive(key) {
		return Hive{}.Classify(key, crawlRoot)
	}
	return d.fallback.Classify(key, crawlRoot)
}

// ---- helpers --------------------------------------------------------------

// dirSegments returns the non-empty directory segments of a key (excludes the
// filename). Backslashes are normalized to '/'.
func dirSegments(key string) []string {
	dir := path.Dir(strings.ReplaceAll(key, "\\", "/"))
	if dir == "." || dir == "/" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(strings.Trim(dir, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitKV parses a "key=value" segment.
func splitKV(seg string) (key, val string, ok bool) {
	i := strings.IndexByte(seg, '=')
	if i <= 0 || i == len(seg)-1 {
		return "", "", false
	}
	return seg[:i], seg[i+1:], true
}

// hasHive reports whether any directory segment is key=value.
func hasHive(key string) bool {
	for _, s := range dirSegments(key) {
		if _, _, ok := splitKV(s); ok {
			return true
		}
	}
	return false
}
