package ui

import (
	"net/http"
	"net/url"

	"github.com/apexion/apexion/internal/dataviewer"
	"github.com/apexion/apexion/internal/model"
)

// insightsSampleCap bounds the DuckDB sample used for column profiling.
const insightsSampleCap = 5000

// This file adds live DuckDB reader-option toggles (Header, Union by name,
// Ignore errors) to the data-preview surfaces — the single-file preview page and
// the dataset "Preview" tab — so a preview can be re-run with different options
// without editing SQL, mirroring the multi-file SQL options drawer.

// previewOptsFromRequest reads reader options from a preview options-bar
// submission. The bar always sends applied=1; without it (the initial lazy
// load) the safe defaults apply, so an unchecked box on first paint doesn't read
// as "off". Filename is always suppressed for previews.
func previewOptsFromRequest(r *http.Request) model.ReadOptions {
	if r.FormValue("applied") != "1" {
		opts := model.DefaultReadOptions()
		opts.Filename = false
		return opts
	}
	opts := model.ReadOptions{Header: "auto"}
	if hv := r.FormValue("header"); hv == "present" || hv == "none" || hv == "auto" {
		opts.Header = hv
	}
	opts.UnionByName = r.FormValue("union_by_name") == "on"
	opts.IgnoreErrors = r.FormValue("ignore_errors") == "on"
	opts.Delimiter = validDelimiter(r.FormValue("delimiter"))
	opts.Filename = false
	return opts.Normalized()
}

// hasPreviewOpts reports whether a format has any toggleable reader option (so
// the bar is hidden for table formats like Iceberg/Delta that ignore them).
func hasPreviewOpts(f model.Format) bool {
	return optHeader(f) || optUnion(f) || optIgnore(f)
}

// optHeader/optUnion/optIgnore gate each toggle to the formats whose DuckDB
// reader actually honours it (see duckdb.readerExprOpts): header is CSV/TSV only;
// union_by_name applies to CSV/TSV/JSON/Parquet; ignore_errors to CSV/TSV/JSON.
func optHeader(f model.Format) bool {
	return f == model.FormatCSV || f == model.FormatTSV
}

func optUnion(f model.Format) bool {
	switch f {
	case model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL, model.FormatParquet:
		return true
	}
	return false
}

func optIgnore(f model.Format) bool {
	switch f {
	case model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL:
		return true
	}
	return false
}

// filePreviewOptsVM builds the options-bar VM for the single-file preview page.
// Format is a visible "Read as" selector (not a hidden field) so an undetected
// file can be pointed at the right reader; only bucket/key are hidden identity.
func filePreviewOptsVM(vm PreviewVM) PreviewOptsVM {
	return PreviewOptsVM{
		Endpoint:   "/ui/preview",
		Method:     "get",
		Target:     "file-preview-result",
		Swap:       "innerHTML",
		Format:     vm.Format,
		Opts:       vm.Opts,
		ShowFormat: true,
		Hidden: [][2]string{
			{"bucket", vm.Bucket},
			{"key", vm.Key},
		},
	}
}

// swapOr returns the hx-swap value, defaulting to innerHTML.
func swapOr(s string) string {
	if s == "" {
		return "innerHTML"
	}
	return s
}

// datasetPreviewOptsVM builds the options-bar VM for the dataset Preview tab. The
// dataset id is carried in the endpoint path, so no hidden identity is needed.
func datasetPreviewOptsVM(id string, format model.Format) PreviewOptsVM {
	opts := model.DefaultReadOptions()
	opts.Filename = false
	return PreviewOptsVM{
		Endpoint: "/ui/datasets/" + id + "/preview",
		Target:   "dataset-preview-result",
		Format:   format,
		Opts:     opts,
	}
}

// partialFilePreview re-runs a single-file preview with the format + reader
// options from the options bar and returns the generated SQL + result table,
// plus an out-of-band refresh of the Query-in-SQL link so it stays in sync with
// the chosen options. An empty/unreadable format yields a guiding message
// rather than DuckDB's raw "preview not supported" error.
func (h *Handler) partialFilePreview(w http.ResponseWriter, r *http.Request) {
	bucket, key := r.FormValue("bucket"), r.FormValue("key")
	if bucket == "" || key == "" {
		h.render(w, r, QueryError("bucket and key required"))
		return
	}
	f := model.Format(r.FormValue("format"))
	opts := previewOptsFromRequest(r)
	if _, ok := fileInitialSQL(bucket, key, f, opts); !ok {
		h.render(w, r, filePreviewErrorFragment(unreadableFormatMsg, bucket, key, f, opts))
		return
	}
	res, err := h.preview.PreviewFileWith(r.Context(), bucket, key, f, opts, 100)
	if err != nil {
		h.render(w, r, filePreviewErrorFragment(err.Error(), bucket, key, f, opts))
		return
	}
	h.render(w, r, filePreviewFragment(res, insightsHref(bucket, key, f, opts), bucket, key, f, opts))
}

// unreadableFormatMsg guides the user when a file's format was not detected (or
// has no DuckDB reader): pick a format above and, for messy logs, ignore errors.
const unreadableFormatMsg = "This file's format wasn't detected. Use “Read as” above to pick a reader " +
	"(e.g. JSON Lines for a gzipped log), and enable “Ignore errors” if some rows are malformed."

// insightsHref builds the lazy-load URL for a single file's column insights. It
// carries bucket/key/format + the reader options (not raw SQL) so the profiler
// reconstructs the reader server-side and stays consistent with the visible
// preview. applied=1 signals previewOptsFromRequest to honour the toggles.
func insightsHref(bucket, key string, f model.Format, opts model.ReadOptions) string {
	v := url.Values{}
	v.Set("bucket", bucket)
	v.Set("key", key)
	v.Set("format", string(f))
	v.Set("applied", "1")
	if opts.Header != "" {
		v.Set("header", opts.Header)
	}
	if opts.UnionByName {
		v.Set("union_by_name", "on")
	}
	if opts.IgnoreErrors {
		v.Set("ignore_errors", "on")
	}
	if opts.Delimiter != "" {
		v.Set("delimiter", opts.Delimiter)
	}
	return "/ui/insights?" + v.Encode()
}

// partialColumnInsights computes column profiles for a single-file preview and
// returns just the populated insight header row, which HTMX swaps in over the
// skeleton row. Errors degrade gracefully to nothing (the name/type header
// remains); the endpoint never blocks the already-rendered data rows.
func (h *Handler) partialColumnInsights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, key := q.Get("bucket"), q.Get("key")
	if bucket == "" || key == "" {
		return
	}
	f := model.Format(q.Get("format"))
	opts := previewOptsFromRequest(r)
	res, err := h.preview.PreviewFileWith(r.Context(), bucket, key, f, opts, 100)
	if err != nil {
		h.log.Warn().Err(err).Msg("insights preview failed")
		return
	}
	src := dataviewer.Source{Bucket: bucket, Key: key, Format: f, Opts: opts}
	profiles, err := h.profiler.Profile(r.Context(), src, res, insightsSampleCap)
	if err != nil {
		h.log.Warn().Err(err).Msg("column profiling failed")
	}
	h.render(w, r, InsightsRow(profiles))
}
