package pricing

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestRates(t *testing.T) {
	// Spot-check published us-east-1 list prices (USD per GB-month).
	for class, want := range map[string]float64{
		Standard: 0.023, StandardIA: 0.0125, OneZoneIA: 0.01, GlacierIR: 0.004,
		Glacier: 0.0036, DeepArchive: 0.00099, IntelligentTiering: 0.023,
	} {
		if got := Rate("us-east-1", class); !near(got, want) {
			t.Errorf("Rate(us-east-1, %s) = %v, want %v", class, got, want)
		}
	}
	if got := Rate("ap-south-1", Standard); !near(got, 0.025) {
		t.Errorf("Rate(ap-south-1, STANDARD) = %v, want 0.025", got)
	}
}

func TestRegionFallback(t *testing.T) {
	for _, r := range []string{"", "garage", "us-east-1"} {
		if got := Region(r); got != DefaultRegion {
			t.Errorf("Region(%q) = %q, want %q", r, got, DefaultRegion)
		}
	}
	if got := Region("eu-west-1"); got != "eu-west-1" {
		t.Errorf("Region(eu-west-1) = %q", got)
	}
	// Empty and unpriced classes are Standard.
	if Rate("us-east-1", "") != Rate("us-east-1", Standard) || Rate("us-east-1", "OUTPOSTS") != Rate("us-east-1", Standard) {
		t.Error("empty/unknown class should be priced as STANDARD")
	}
}

func TestObjectCost(t *testing.T) {
	const kb, gib = 1 << 10, 1 << 30
	cases := []struct {
		name  string
		class string
		size  int64
		want  float64
	}{
		{"1 GiB standard", Standard, gib, 0.023},
		{"lowercase class", "standard", gib, 0.023},
		{"1 KiB standard is not rounded up", Standard, kb, 0.023 * kb / gib},
		{"1 KiB standard-IA bills 128 KiB", StandardIA, kb, 0.0125 * 128 * kb / gib},
		{"1 GiB standard-IA", StandardIA, gib, 0.0125},
		{"glacier adds 32 KiB + 8 KiB standard", Glacier, gib, (gib*0.0036 + 32*kb*0.0036 + 8*kb*0.023) / gib},
		{"deep archive adds overhead", DeepArchive, 0, (32*kb*0.00099 + 8*kb*0.023) / gib},
	}
	for _, c := range cases {
		if got := ObjectCost("us-east-1", c.class, c.size); !near(got, c.want) {
			t.Errorf("%s: ObjectCost = %v, want %v", c.name, got, c.want)
		}
	}
}
