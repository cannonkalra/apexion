package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/apexion/apexion/internal/model"
)

// TestFileQueryURL verifies the "Query in SQL" link targets the SQL console
// (/query) — not the non-existent /sql route — carrying the file identity so
// the editor can regenerate the SQL, and reports non-queryable formats so the
// button renders disabled.
func TestFileQueryURL(t *testing.T) {
	cases := []struct {
		name   string
		format model.Format
		wantOK bool
	}{
		{"csv", model.FormatCSV, true},
		{"tsv", model.FormatTSV, true},
		{"parquet", model.FormatParquet, true},
		{"json", model.FormatJSON, true},
		{"jsonl", model.FormatJSONL, true},
		{"iceberg", model.FormatIceberg, true},
		{"delta", model.FormatDelta, true},
		// Formats without a native DuckDB reader must not offer the button.
		{"avro", model.FormatAvro, false},
		{"orc", model.FormatORC, false},
		{"unknown", model.FormatUnknown, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := fileQueryURL("logs", "2025/data."+c.name, c.format, model.DefaultReadOptions())
			if ok != c.wantOK {
				t.Fatalf("fileQueryURL ok=%v want %v (url=%q)", ok, c.wantOK, got)
			}
			if !ok {
				if got != "" {
					t.Errorf("non-queryable format returned url %q, want empty", got)
				}
				return
			}
			if !strings.HasPrefix(got, "/query?") {
				t.Errorf("link must target /query, got %q", got)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("link is not a valid URL: %v", err)
			}
			qv := u.Query()
			if qv.Get("bucket") != "logs" || qv.Get("key") != "2025/data."+c.name {
				t.Errorf("link missing file identity: %q", got)
			}
			if qv.Get("format") != string(c.format) {
				t.Errorf("link format = %q, want %q", qv.Get("format"), c.format)
			}
		})
	}
}

// TestFileQueryURLCarriesOptions verifies that non-default reader options ride
// along in the link (so a format override + Ignore errors chosen on the preview
// reaches the editor), and default options stay off the URL.
func TestFileQueryURLCarriesOptions(t *testing.T) {
	opts := model.ReadOptions{Header: "none", IgnoreErrors: true, UnionByName: false}
	got, ok := fileQueryURL("b", "logs/2026/ads.log.gz", model.FormatJSONL, opts)
	if !ok {
		t.Fatal("jsonl must be queryable")
	}
	qv, _ := url.ParseQuery(strings.TrimPrefix(got, "/query?"))
	if qv.Get("ignore_errors") != "on" {
		t.Errorf("ignore_errors not carried: %q", got)
	}
	if qv.Get("header") != "none" {
		t.Errorf("header not carried: %q", got)
	}
	if qv.Get("union_by_name") != "" {
		t.Errorf("default union_by_name should be omitted: %q", got)
	}
}

// TestFileInitialSQL verifies the editor prefill uses the right DuckDB reader
// per format and honors ignore_errors — the gzipped-log-as-JSONL case the
// feature was built for.
func TestFileInitialSQL(t *testing.T) {
	// A gzipped log read as JSON Lines with ignore_errors: DuckDB decompresses
	// .gz by extension, so the reader just needs read_json_auto + ignore_errors.
	sql, ok := fileInitialSQL("bkt",
		"AroscopLog/adservinglog/2026-07-01-21/07/ads.2.virginia.log.2026-07-01-21-06.gz",
		model.FormatJSONL, model.ReadOptions{Header: "none", IgnoreErrors: true})
	if !ok {
		t.Fatal("jsonl should be readable")
	}
	if !strings.Contains(sql, "read_json_auto(") || !strings.Contains(sql, "ignore_errors=true") {
		t.Errorf("expected read_json_auto with ignore_errors, got %q", sql)
	}
	if !strings.HasSuffix(sql, "LIMIT 100") {
		t.Errorf("expected bounded SELECT, got %q", sql)
	}
	// An unreadable format yields no SQL, so the editor opens empty.
	if _, ok := fileInitialSQL("b", "x.orc", model.FormatORC, model.DefaultReadOptions()); ok {
		t.Error("ORC must not produce prefilled SQL")
	}
}

// TestQueryInitialSQL verifies the query-page prefill precedence: a catalog
// ?table= wins and selects the sidebar; otherwise ?sql= is used verbatim with
// no sidebar selection; with neither, the editor stays empty.
func TestQueryInitialSQL(t *testing.T) {
	cases := []struct {
		name         string
		table, sql   string
		wantInitial  string
		wantSelected string
	}{
		{"table wins", "events", "SELECT 1", "SELECT * FROM events LIMIT 100", "events"},
		{"sql only", "", "SELECT * FROM read_parquet('s3://b/k') LIMIT 100", "SELECT * FROM read_parquet('s3://b/k') LIMIT 100", ""},
		{"neither", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			initial, selected := queryInitialSQL(c.table, c.sql)
			if initial != c.wantInitial {
				t.Errorf("initial = %q, want %q", initial, c.wantInitial)
			}
			if selected != c.wantSelected {
				t.Errorf("selected = %q, want %q", selected, c.wantSelected)
			}
		})
	}
}
