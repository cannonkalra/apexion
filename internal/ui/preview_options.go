package ui

import (
	"net/http"

	"github.com/apexion/apexion/internal/model"
)

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
func filePreviewOptsVM(vm PreviewVM) PreviewOptsVM {
	return PreviewOptsVM{
		Endpoint: "/ui/preview",
		Target:   "file-preview-result",
		Format:   vm.Format,
		Opts:     vm.Opts,
		Hidden: [][2]string{
			{"bucket", vm.Bucket},
			{"key", vm.Key},
			{"format", string(vm.Format)},
		},
	}
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

// partialFilePreview re-runs a single-file preview with reader options from the
// options bar and returns the generated SQL + result table.
func (h *Handler) partialFilePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, key := q.Get("bucket"), q.Get("key")
	if bucket == "" || key == "" {
		h.render(w, r, QueryError("bucket and key required"))
		return
	}
	f := model.Format(q.Get("format"))
	res, err := h.preview.PreviewFileWith(r.Context(), bucket, key, f, previewOptsFromRequest(r), 100)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, PreviewResult(res))
}
