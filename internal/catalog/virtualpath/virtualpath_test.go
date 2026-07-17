package virtualpath

import (
	"strings"
	"testing"
)

func names(ps []Partition) string {
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Name + "=" + p.Value)
	}
	return b.String()
}

func TestBuild(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		depth     int
		wantVirt  string
		wantFile  string
		wantParts string // "name=value,name=value"
	}{
		{"one partition", "2023/file.parquet", 1, "pt0=2023/file.parquet", "file.parquet", "pt0=2023"},
		{"two partitions", "US/IDFA/file.csv", 2, "pt0=US/pt1=IDFA/file.csv", "file.csv", "pt0=US,pt1=IDFA"},
		{"three (eyeota)", "2023-10-11/US/IDFA/file.parquet", 3, "pt0=2023-10-11/pt1=US/pt2=IDFA/file.parquet", "file.parquet", "pt0=2023-10-11,pt1=US,pt2=IDFA"},
		{"nested/arbitrary depth 6", "year/month/day/hour/device/country/f", 6, "pt0=year/pt1=month/pt2=day/pt3=hour/pt4=device/pt5=country/f", "f", "pt0=year,pt1=month,pt2=day,pt3=hour,pt4=device,pt5=country"},
		{"no directories", "file.parquet", 3, "file.parquet", "file.parquet", ""},
		{"depth clamps to dirs", "a/b/file", 10, "pt0=a/pt1=b/file", "file", "pt0=a,pt1=b"},
		{"depth less than dirs keeps rest literal", "a/b/c/file", 1, "pt0=a/b/c/file", "file", "pt0=a"},
		{"depth zero", "a/b/file", 0, "a/b/file", "file", ""},
		{"negative depth", "a/b/file", -3, "a/b/file", "file", ""},
		{"spaces in dirs and file", "New York/San Jose/my file.csv", 2, "pt0=New York/pt1=San Jose/my file.csv", "my file.csv", "pt0=New York,pt1=San Jose"},
		{"utf-8", "日本/東京/ファイル.parquet", 2, "pt0=日本/pt1=東京/ファイル.parquet", "ファイル.parquet", "pt0=日本,pt1=東京"},
		{"hidden file", "logs/US/.hidden", 2, "pt0=logs/pt1=US/.hidden", ".hidden", "pt0=logs,pt1=US"},
		{"filename with dots", "a/b/archive.tar.gz", 2, "pt0=a/pt1=b/archive.tar.gz", "archive.tar.gz", "pt0=a,pt1=b"},
		{"equals in filename is not a partition", "a/b/wei=rd.parquet", 2, "pt0=a/pt1=b/wei=rd.parquet", "wei=rd.parquet", "pt0=a,pt1=b"},
		{"equals in directory value preserved", "k=v/US/file", 2, "pt0=k=v/pt1=US/file", "file", "pt0=k=v,pt1=US"},
		{"leading slash", "/a/b/file", 2, "pt0=a/pt1=b/file", "file", "pt0=a,pt1=b"},
		{"windows separators", "a\\b\\c\\file.parquet", 3, "pt0=a/pt1=b/pt2=c/file.parquet", "file.parquet", "pt0=a,pt1=b,pt2=c"},
		{"empty folders collapse", "a//b///c/file", 3, "pt0=a/pt1=b/pt2=c/file", "file", "pt0=a,pt1=b,pt2=c"},
		{"trailing slash (empty filename)", "a/b/c/", 3, "pt0=a/pt1=b/pt2=c/", "", "pt0=a,pt1=b,pt2=c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildVirtualPath(c.key, c.depth)
			if got.Virtual != c.wantVirt {
				t.Errorf("Virtual = %q, want %q", got.Virtual, c.wantVirt)
			}
			if got.Filename != c.wantFile {
				t.Errorf("Filename = %q, want %q", got.Filename, c.wantFile)
			}
			if n := names(got.Partitions); n != c.wantParts {
				t.Errorf("Partitions = %q, want %q", n, c.wantParts)
			}
		})
	}
}

func TestTenPartitions(t *testing.T) {
	segs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	key := strings.Join(segs, "/") + "/file.parquet"
	got := BuildVirtualPath(key, 10)
	if len(got.Partitions) != 10 {
		t.Fatalf("want 10 partitions, got %d", len(got.Partitions))
	}
	for i, p := range got.Partitions {
		if p.Name != "pt"+itoa(i) || p.Value != segs[i] {
			t.Errorf("partition %d = %+v", i, p)
		}
	}
	if !strings.HasPrefix(got.Virtual, "pt0=a/") || !strings.HasSuffix(got.Virtual, "/file.parquet") {
		t.Errorf("virtual = %q", got.Virtual)
	}
}

func TestConfigurablePrefixAndSeparator(t *testing.T) {
	// separator "_" → pt_0, pt_1
	b := New("pt", "_")
	got := b.Build("2023/US/f", 2)
	if got.Virtual != "pt_0=2023/pt_1=US/f" {
		t.Errorf("sep: Virtual = %q", got.Virtual)
	}
	if got.Partitions[0].Name != "pt_0" || got.Partitions[1].Name != "pt_1" {
		t.Errorf("sep: names = %+v", got.Partitions)
	}
	// custom prefix
	if v := New("part", "_").Build("a/f", 1).Virtual; v != "part_0=a/f" {
		t.Errorf("prefix: %q", v)
	}
	// empty prefix defaults to "pt"
	if v := New("", "").Build("a/f", 1).Virtual; v != "pt0=a/f" {
		t.Errorf("default prefix: %q", v)
	}
}

func TestOriginalPreserved(t *testing.T) {
	got := BuildVirtualPath("/2023-10-11/US/IDFA/file.parquet", 3)
	if got.Original != "2023-10-11/US/IDFA/file.parquet" {
		t.Errorf("Original = %q", got.Original)
	}
}

func TestTranslateRoundTrip(t *testing.T) {
	b := New("pt", "")
	keys := []string{
		"2023-10-11/US/IDFA/file.parquet",
		"a/b/c/d/file",
		"k=v/US/file", // partition value containing '='
		"file.parquet",
		"a/b/wei=rd.parquet", // '=' in the filename must survive
	}
	for _, k := range keys {
		v := b.Build(k, 1<<30)
		if got := b.Translate(v.Virtual); got != v.Original {
			t.Errorf("Translate(Build(%q)) = %q, want %q", k, got, v.Original)
		}
	}
	// Separator variant.
	bs := New("pt", "_")
	v := bs.Build("2023/US/f", 2)
	if got := bs.Translate(v.Virtual); got != "2023/US/f" {
		t.Errorf("sep translate = %q", got)
	}
}

func TestItoa(t *testing.T) {
	for n, want := range map[int]string{0: "0", 5: "5", 10: "10", 123: "123", 9999: "9999"} {
		if got := itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}
