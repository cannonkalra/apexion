package crawler

import (
	"testing"

	"github.com/apexion/apexion/internal/catalog/discovery"
	"github.com/apexion/apexion/internal/model"
)

func TestDeriveDataset(t *testing.T) {
	disp := discovery.New(discovery.ModePositional, "pt", "")
	cases := []struct {
		key       string
		crawlRoot string
		wantRoot  string
		wantStrat string
		wantKeys  []string
	}{
		// Hive is detected and unchanged (backward compatible).
		{"orders/year=2023/month=01/part-0.parquet", "", "orders", "hive", []string{"year", "month"}},
		// Bare directories become positional partitions rooted at the crawl root.
		{"2023-10-11/US/IDFA/file.parquet", "", "", "positional", []string{"pt0", "pt1", "pt2"}},
		{"feed/2023/US/f.parquet", "feed", "feed", "positional", []string{"pt0", "pt1"}},
		// A file directly under the crawl root has no partitions.
		{"top.csv", "", "", "positional", nil},
	}
	for _, c := range cases {
		got := deriveDataset(c.key, c.crawlRoot, disp)
		if got.root != c.wantRoot {
			t.Errorf("deriveDataset(%q).root = %q, want %q", c.key, got.root, c.wantRoot)
		}
		if got.strategy != c.wantStrat {
			t.Errorf("deriveDataset(%q).strategy = %q, want %q", c.key, got.strategy, c.wantStrat)
		}
		if len(got.keys) != len(c.wantKeys) {
			t.Errorf("deriveDataset(%q).keys = %v, want %v", c.key, got.keys, c.wantKeys)
			continue
		}
		for i := range c.wantKeys {
			if got.keys[i] != c.wantKeys[i] {
				t.Errorf("deriveDataset(%q).keys[%d] = %q, want %q", c.key, i, got.keys[i], c.wantKeys[i])
			}
		}
	}
}

func TestTableMarker(t *testing.T) {
	cases := []struct {
		key      string
		wantRoot string
		wantFmt  model.Format
		wantOK   bool
	}{
		{"sales/_delta_log/00000000000000000000.json", "sales", model.FormatDelta, true},
		{"lake/tbl/metadata/v3.metadata.json", "lake/tbl", model.FormatIceberg, true},
		{"orders/year=2023/part.parquet", "", model.FormatUnknown, false},
	}
	for _, c := range cases {
		root, f, ok := tableMarker(c.key)
		if ok != c.wantOK || root != c.wantRoot || f != c.wantFmt {
			t.Errorf("tableMarker(%q) = (%q,%s,%v), want (%q,%s,%v)",
				c.key, root, f, ok, c.wantRoot, c.wantFmt, c.wantOK)
		}
	}
}

func TestShouldIgnore(t *testing.T) {
	ignore := []string{".hidden", "data/_SUCCESS", "x/_temporary/0/part", "dir/"}
	keep := []string{"data.csv", "a/b/file.parquet"}
	for _, k := range ignore {
		if !shouldIgnore(k, true) {
			t.Errorf("shouldIgnore(%q) = false, want true", k)
		}
	}
	for _, k := range keep {
		if shouldIgnore(k, true) {
			t.Errorf("shouldIgnore(%q) = true, want false", k)
		}
	}
}

func TestDominantFormat(t *testing.T) {
	votes := map[model.Format]int{model.FormatCSV: 2, model.FormatParquet: 5}
	if got := dominantFormat(votes); got != model.FormatParquet {
		t.Errorf("dominantFormat = %s, want parquet", got)
	}
}
