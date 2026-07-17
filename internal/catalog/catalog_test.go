package catalog

import "testing"

func TestSanitizeTableName(t *testing.T) {
	cases := map[string]string{
		"events":           "events",
		"Web Events":       "web_events",
		"logs/2026":        "logs_2026",
		"2026-logs":        "t_2026_logs",
		"my.dataset.name":  "my_dataset_name",
		"  spaced  ":       "spaced",
		"!!!":              "table",
		"CamelCaseDataset": "camelcasedataset",
		"a__b":             "a_b",
	}
	for in, want := range cases {
		if got := SanitizeTableName(in); got != want {
			t.Errorf("SanitizeTableName(%q) = %q, want %q", in, got, want)
		}
	}
}
