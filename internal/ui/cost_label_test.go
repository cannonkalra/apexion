package ui

import "testing"

func TestCostLabel(t *testing.T) {
	cases := []struct {
		usd       float64
		truncated bool
		want      string
	}{
		{0, false, "$0.00"},
		{0.0000004, false, "<$0.01"},
		{0.01, false, "$0.01"},
		{12.345, false, "$12.35"},
		{1234567.891, false, "$1,234,567.89"},
		{999.999, false, "$1,000.00"},
		{3.5, true, "≥ $3.50"},
	}
	for _, c := range cases {
		if got := costLabel(c.usd, c.truncated); got != c.want {
			t.Errorf("costLabel(%v, %v) = %q, want %q", c.usd, c.truncated, got, c.want)
		}
	}
}

func TestExactUSD(t *testing.T) {
	for usd, want := range map[float64]string{0: "$0.00", 0.0000000214321: "$0.0000000214", 0.004567: "$0.00457", 1234.5: "$1,234.50"} {
		if got := exactUSD(usd); got != want {
			t.Errorf("exactUSD(%v) = %q, want %q", usd, got, want)
		}
	}
}
