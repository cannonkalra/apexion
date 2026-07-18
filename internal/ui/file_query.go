package ui

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
)

// This file backs "Query in SQL" — opening a single previewed object in the SQL
// editor pre-populated with a SELECT over its DuckDB reader. The editor carries
// the file's format and reader options (header, union_by_name, ignore_errors)
// and exposes them as a live bar, so a misdetected or unknown-extension file
// (e.g. a gzipped log with no .jsonl suffix) can be coerced into the right
// reader — "read as JSON Lines" + "ignore errors" — without hand-editing SQL.

// fileInitialSQL builds the editor's prefilled statement: a bounded SELECT over
// the file's reader expression. The filename column is always suppressed (a
// single file needs none). ok is false when the format has no native DuckDB
// reader (e.g. Avro, ORC, or an undetected format), so the caller can gate the
// button / leave the editor empty rather than emit a query that cannot run.
func fileInitialSQL(bucket, key string, f model.Format, opts model.ReadOptions) (string, bool) {
	o := opts
	o.Filename = false
	from, err := duckdb.FromClause(bucket, key, f, o)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("SELECT * FROM %s LIMIT 100", from), true
}

// fileQueryValues encodes a file's identity and reader options into the query
// string shared by the "Query in SQL" link and the editor's options bar. Only
// non-default options are emitted, keeping the URL readable.
func fileQueryValues(bucket, key string, f model.Format, opts model.ReadOptions) url.Values {
	v := url.Values{}
	v.Set("bucket", bucket)
	v.Set("key", key)
	v.Set("format", string(f))
	if opts.Header == "present" || opts.Header == "none" {
		v.Set("header", opts.Header)
	}
	if opts.UnionByName {
		v.Set("union_by_name", "on")
	}
	if opts.IgnoreErrors {
		v.Set("ignore_errors", "on")
	}
	return v
}

// fileQueryURL builds the "Query in SQL" link for a previewed file: it opens the
// SQL console (/query) with the file's identity + reader options, so the editor
// regenerates the prefilled SQL from them. ok mirrors fileInitialSQL: false for
// formats DuckDB cannot read directly, so the caller renders a disabled button.
func fileQueryURL(bucket, key string, f model.Format, opts model.ReadOptions) (string, bool) {
	if _, ok := fileInitialSQL(bucket, key, f, opts); !ok {
		return "", false
	}
	return "/query?" + fileQueryValues(bucket, key, f, opts).Encode(), true
}

// fileOptsFromRequest reads reader options for a single-file surface from either
// a GET query (the "Query in SQL" link) or a POST form (the editor options bar);
// r.FormValue covers both. Filename is always suppressed for a single file.
func fileOptsFromRequest(r *http.Request) model.ReadOptions {
	opts := model.ReadOptions{Header: "auto"}
	if hv := r.FormValue("header"); hv == "present" || hv == "none" || hv == "auto" {
		opts.Header = hv
	}
	opts.UnionByName = r.FormValue("union_by_name") == "on"
	opts.IgnoreErrors = r.FormValue("ignore_errors") == "on"
	opts.Filename = false
	return opts.Normalized()
}

// parseFileQuery reconstructs the single-file editor state from a request.
func parseFileQuery(r *http.Request) QueryFileVM {
	return QueryFileVM{
		Bucket: r.FormValue("bucket"),
		Key:    r.FormValue("key"),
		Format: model.Format(r.FormValue("format")),
		Opts:   fileOptsFromRequest(r),
	}
}

// fileEditorOptsVM drives the SQL editor's reader-options bar for a single file.
// On change it POSTs to /ui/query/file-options, which regenerates the editor
// (SqlEditorAndSchema) with SQL reflecting the new format/options.
func fileEditorOptsVM(f QueryFileVM) PreviewOptsVM {
	return PreviewOptsVM{
		Endpoint:   "/ui/query/file-options",
		Method:     "post",
		Target:     "sql-editor-and-schema",
		Swap:       "outerHTML",
		Format:     f.Format,
		Opts:       f.Opts,
		ShowFormat: true,
		Hidden:     [][2]string{{"bucket", f.Bucket}, {"key", f.Key}},
	}
}

// readAsCurrent maps a file's format to the value pre-selected in the "Read as"
// dropdown. Formats without an offered reader (undetected, Avro, ORC) map to the
// empty placeholder so the user is prompted to choose one.
func readAsCurrent(f model.Format) string {
	switch f {
	case model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL, model.FormatParquet:
		return string(f)
	default:
		return ""
	}
}

// actionQueryFileOptions regenerates the single-file SQL editor when the reader
// format or a toggle changes, swapping #sql-editor-and-schema with SQL built
// from the new options. The options bar itself sits outside the swapped node, so
// its controls keep their state across regenerations.
func (h *Handler) actionQueryFileOptions(w http.ResponseWriter, r *http.Request) {
	f := parseFileQuery(r)
	vm := QueryVM{Ready: h.preview.Ready(), File: &f}
	vm.InitialSQL, _ = fileInitialSQL(f.Bucket, f.Key, f.Format, f.Opts)
	h.render(w, r, SqlEditorAndSchema(vm))
}
