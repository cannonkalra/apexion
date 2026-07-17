package discovery

import (
	"strings"
	"testing"
)

func parts(c Classification) string {
	var b strings.Builder
	for i, p := range c.Partitions {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Name + "=" + p.Value)
	}
	return b.String()
}

func TestPositionalSuccessCase(t *testing.T) {
	// The four files must all resolve to the SAME root ("" = crawl root) so the
	// aggregate groups them into one dataset, each with pt0/pt1/pt2.
	d := New(ModePositional, "pt", "")
	keys := []string{
		"2023-10-11/US/IDFA/file1.parquet",
		"2023-10-11/US/IDFA/file2.parquet",
		"2023-10-11/CA/IDFA/file3.parquet",
		"2023-10-12/US/IDFA/file4.parquet",
	}
	wantParts := []string{
		"pt0=2023-10-11,pt1=US,pt2=IDFA",
		"pt0=2023-10-11,pt1=US,pt2=IDFA",
		"pt0=2023-10-11,pt1=CA,pt2=IDFA",
		"pt0=2023-10-12,pt1=US,pt2=IDFA",
	}
	for i, k := range keys {
		c := d.Classify(k, "")
		if c.Root != "" {
			t.Errorf("%s: root = %q, want \"\"", k, c.Root)
		}
		if c.Strategy != StrategyPositional {
			t.Errorf("%s: strategy = %q", k, c.Strategy)
		}
		if got := parts(c); got != wantParts[i] {
			t.Errorf("%s: parts = %q, want %q", k, got, wantParts[i])
		}
	}
	// depth 3 for all → one dataset with 3 partition columns
	if len(d.Classify(keys[0], "").Keys()) != 3 {
		t.Error("expected partition depth 3")
	}
}

func TestPositionalRelativeToCrawlRoot(t *testing.T) {
	d := New(ModePositional, "pt", "")
	c := d.Classify("eyeota-data-feed/2023-10-11/US/IDFA/f.parquet", "eyeota-data-feed")
	if c.Root != "eyeota-data-feed" {
		t.Errorf("root = %q", c.Root)
	}
	if got := parts(c); got != "pt0=2023-10-11,pt1=US,pt2=IDFA" {
		t.Errorf("parts = %q", got)
	}
}

func TestSingleLevel(t *testing.T) {
	c := New(ModePositional, "pt", "").Classify("2023/file.parquet", "")
	if parts(c) != "pt0=2023" || len(c.Partitions) != 1 {
		t.Errorf("single level: %q", parts(c))
	}
}

func TestFileDirectlyUnderRoot(t *testing.T) {
	// No directories → no partitions, root is the crawl root.
	c := New(ModePositional, "pt", "").Classify("file.parquet", "")
	if len(c.Partitions) != 0 || c.Root != "" {
		t.Errorf("root file: parts=%q root=%q", parts(c), c.Root)
	}
}

func TestDeepTree(t *testing.T) {
	c := New(ModePositional, "pt", "").Classify("year/month/day/hour/device/country/f.parquet", "")
	if len(c.Partitions) != 6 {
		t.Fatalf("want depth 6, got %d", len(c.Partitions))
	}
	if parts(c) != "pt0=year,pt1=month,pt2=day,pt3=hour,pt4=device,pt5=country" {
		t.Errorf("deep: %q", parts(c))
	}
}

func TestConfigurableNaming(t *testing.T) {
	c := New(ModePositional, "part", "_").Classify("2023/US/f", "")
	if parts(c) != "part_0=2023,part_1=US" {
		t.Errorf("naming: %q", parts(c))
	}
}

func TestHiveRegression(t *testing.T) {
	// Hive datasets must classify exactly as before: root = non-kv prefix,
	// trailing key=value dirs are the partitions.
	d := New(ModePositional, "pt", "") // even in positional mode, hive is detected
	c := d.Classify("logs/date=2023-10-11/country=US/file.parquet", "")
	if c.Strategy != StrategyHive {
		t.Errorf("strategy = %q, want hive", c.Strategy)
	}
	if c.Root != "logs" {
		t.Errorf("root = %q, want logs", c.Root)
	}
	if parts(c) != "date=2023-10-11,country=US" {
		t.Errorf("hive parts = %q", parts(c))
	}
}

func TestHiveDirectHive(t *testing.T) {
	c := Hive{}.Classify("dt=2023/hr=00/f.parquet", "")
	if c.Root != "" || parts(c) != "dt=2023,hr=00" {
		t.Errorf("hive: root=%q parts=%q", c.Root, parts(c))
	}
}

func TestMixedLayoutInBucket(t *testing.T) {
	// One bucket, one hive dataset and one positional dataset: each key routes to
	// the right strategy.
	d := New(ModePositional, "pt", "")
	hive := d.Classify("sales/dt=2023/f.parquet", "")
	pos := d.Classify("events/2023/US/f.parquet", "")
	if hive.Strategy != StrategyHive || hive.Root != "sales" {
		t.Errorf("hive branch: %+v", hive)
	}
	if pos.Strategy != StrategyPositional {
		t.Errorf("positional branch: %+v", pos)
	}
}

func TestHiveModeUsesLegacyForNonHive(t *testing.T) {
	// In hive mode, non-hive paths fall back to legacy (leaf = dataset, no parts).
	d := New(ModeHive, "pt", "")
	c := d.Classify("a/b/c/file.parquet", "")
	if c.Strategy != StrategyLegacy {
		t.Errorf("strategy = %q, want legacy", c.Strategy)
	}
	if c.Root != "a/b/c" {
		t.Errorf("legacy root = %q, want a/b/c", c.Root)
	}
	if len(c.Partitions) != 0 {
		t.Errorf("legacy must have no partitions: %q", parts(c))
	}
	// Hive still detected even in hive mode.
	if h := d.Classify("a/k=v/f", ""); h.Strategy != StrategyHive {
		t.Errorf("hive not detected in hive mode: %+v", h)
	}
}

func TestFormatAgnostic(t *testing.T) {
	// Classification depends only on directory structure, not file format.
	d := New(ModePositional, "pt", "")
	for _, f := range []string{"f.parquet", "f.csv", "f.json", "f.avro", "f.orc"} {
		if p := parts(d.Classify("2023/US/"+f, "")); p != "pt0=2023,pt1=US" {
			t.Errorf("%s: %q", f, p)
		}
	}
}

func TestEmptyAndEdgePaths(t *testing.T) {
	d := New(ModePositional, "pt", "")
	// empty directories / double slashes collapse
	if p := parts(d.Classify("a//b///c/file", "")); p != "pt0=a,pt1=b,pt2=c" {
		t.Errorf("collapse: %q", p)
	}
	// leading slash + windows separators
	if p := parts(d.Classify("/a\\b/file", "")); p != "pt0=a,pt1=b" {
		t.Errorf("normalize: %q", p)
	}
	// key equal to crawl root prefix, files right under it
	if p := parts(d.Classify("root/f.parquet", "root")); p != "" {
		t.Errorf("under root: %q", p)
	}
}

func TestNamesAndAutoMode(t *testing.T) {
	if (Hive{}).Name() != StrategyHive || (Legacy{}).Name() != StrategyLegacy {
		t.Error("strategy names")
	}
	if NewPositional("pt", "").Name() != StrategyPositional {
		t.Error("positional name")
	}
	d := New(ModeAuto, "pt", "")
	if !strings.Contains(d.Name(), "positional") {
		t.Errorf("dispatcher name = %q", d.Name())
	}
	// auto behaves as positional for non-hive
	if p := parts(d.Classify("2023/US/f", "")); p != "pt0=2023,pt1=US" {
		t.Errorf("auto: %q", p)
	}
}

func TestKeysHelper(t *testing.T) {
	c := New(ModePositional, "pt", "").Classify("a/b/f", "")
	k := c.Keys()
	if len(k) != 2 || k[0] != "pt0" || k[1] != "pt1" {
		t.Errorf("keys = %v", k)
	}
}
