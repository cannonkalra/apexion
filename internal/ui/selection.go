package ui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection"
)

// selKey is the stable client/server identity of a selected file. It MUST match
// the data-file attribute the explorer JS builds (bucket, key, format joined by
// spaces), so OOB row badges and the JS selection Set agree.
func selKey(bucket, key, format string) string {
	return bucket + " " + key + " " + format
}

// rowBadgeID is the DOM id of a file row's compatibility-badge slot. Derived
// from selKey via a short hash so it is a valid HTML id regardless of the key's
// characters (slashes, spaces, unicode).
func rowBadgeID(bucket, key, format string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(selKey(bucket, key, format)))
	return "badge-" + strconv.FormatUint(uint64(h.Sum32()), 16)
}

// actionSelectionSet creates or replaces the selection session for the posted
// files and returns the footer selection bar (which then polls for progress). An
// empty selection collapses the bar.
func (h *Handler) actionSelectionSet(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	var files []selection.FileRef
	if raw := r.FormValue("files"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &files); err != nil {
			h.log.Warn().Err(err).Msg("selection: bad files payload")
		}
	}
	if len(files) == 0 {
		if token != "" {
			h.selection.Drop(token)
		}
		h.render(w, r, SelectionBarEmpty())
		return
	}
	// Reader options come from the Explorer options strip (no header / union /
	// ignore errors) so discovery reads the files that way from the start.
	tok, snap := h.selection.SetWith(token, files, readOptsFromForm(r))
	h.render(w, r, SelectionBar(tok, snap))
}

// partialSelectionProgress is polled by the bar until discovery completes.
func (h *Handler) partialSelectionProgress(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	snap, ok := h.selection.Progress(token)
	if !ok {
		h.render(w, r, SelectionExpired())
		return
	}
	h.render(w, r, SelectionProgress(token, snap))
}

// actionSelectionRegenerate applies changed reader options and re-renders the
// SQL editor + schema preview. Because options like Header change how each
// file's schema is inferred (a headerless CSV mis-groups under auto-detection),
// this re-runs schema discovery so both the grouping/schema AND the generated
// SQL reflect the new options.
func (h *Handler) actionSelectionRegenerate(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	// An optional "Read as" override forces every selected file to a single
	// format (empty = keep each file's detected format), so a mixed/misdetected
	// selection can be coerced — e.g. all read as CSV with a chosen delimiter.
	override := selectableFormat(model.Format(r.FormValue("format")))
	sum, ok := h.selection.RediscoverSyncAs(token, readOptsFromForm(r), override)
	if !ok || sum == nil {
		h.render(w, r, SqlEditorAndSchema(QueryVM{Sel: token, SelExpired: true, Ready: h.preview.Ready()}))
		return
	}
	h.render(w, r, SqlEditorAndSchema(querySelVM(token, sum, h.preview.Ready())))
}

// actionSelectionOptions re-runs discovery for the footer selection with new
// reader options (Header/Union/Ignore) and returns the refreshed bar — so a
// selection of headerless CSVs can be made compatible before opening the editor.
func (h *Handler) actionSelectionOptions(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	tok, snap, ok := h.selection.Reoptions(token, readOptsFromForm(r))
	if !ok {
		h.render(w, r, SelectionBarEmpty())
		return
	}
	h.render(w, r, SelectionBar(tok, snap))
}

// partialSelectionPreview runs the combined selection SQL and returns the
// floating preview body (result table + save form).
func (h *Handler) partialSelectionPreview(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	snap, ok := h.selection.Progress(token)
	if !ok || snap.Summary == nil {
		h.render(w, r, QueryError("This selection is no longer available."))
		return
	}
	pt := snap.Summary.PrimaryTable()
	if pt == nil || pt.SQL == "" {
		h.render(w, r, QueryError("No compatible files to preview."))
		return
	}
	res, err := h.catalog.Query(r.Context(), pt.SQL, 100)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, SelectionPreviewBody(token, pt.Name, res))
}

// actionSelectionSave saves the combined selection as a queryable SQL table
// (a DuckDB view over the file list) and redirects to it in the SQL editor.
func (h *Handler) actionSelectionSave(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	name := r.FormValue("name")
	snap, ok := h.selection.Progress(token)
	if !ok || snap.Summary == nil {
		h.render(w, r, Toast("This selection is no longer available.", "error"))
		return
	}
	pt := snap.Summary.PrimaryTable()
	if pt == nil || pt.SQL == "" {
		h.render(w, r, Toast("No compatible files to save.", "error"))
		return
	}
	if name == "" {
		name = pt.Name
	}
	entry, err := h.catalog.RegisterSelectionView(r.Context(), name, pt.SQL, pt.Format)
	if err != nil {
		h.render(w, r, Toast("Save failed: "+err.Error(), "error"))
		return
	}
	w.Header().Set("HX-Redirect", "/query?table="+entry.Name)
	h.render(w, r, Toast("Saved table "+entry.Name, "success"))
}

// actionSelectionClear discards the selection session and collapses the bar.
func (h *Handler) actionSelectionClear(w http.ResponseWriter, r *http.Request) {
	h.selection.Drop(r.FormValue("token"))
	h.render(w, r, SelectionBarEmpty())
}

// readOptsFromForm builds DuckDB reader options from the SQL options drawer.
func readOptsFromForm(r *http.Request) model.ReadOptions {
	opts := model.ReadOptions{
		Header:       "auto",
		UnionByName:  r.FormValue("union_by_name") == "on",
		Filename:     r.FormValue("filename") == "on",
		IgnoreErrors: r.FormValue("ignore_errors") == "on",
	}
	if hv := r.FormValue("header"); hv == "present" || hv == "none" {
		opts.Header = hv
	}
	// The compact selection control uses a single "Ignore headers" checkbox
	// (no_header) instead of the 3-state Header select.
	if r.FormValue("no_header") == "on" {
		opts.Header = "none"
	}
	if n, err := strconv.Atoi(r.FormValue("sample_size")); err == nil && n >= 0 {
		opts.SampleSize = n
	}
	opts.Delimiter = validDelimiter(r.FormValue("delimiter"))
	return opts.Normalized()
}

// selTokenVals builds the hx-vals JSON payload carrying the selection token.
func selTokenVals(token string) string {
	b, _ := json.Marshal(map[string]string{"token": token})
	return string(b)
}

// compatTitle is the hover tooltip for a row badge (e.g. "price: DOUBLE vs
// VARCHAR" or a discovery error).
func compatTitle(f selection.FileState) string {
	switch f.Verdict {
	case selection.VerdictCompatible:
		return "Compatible"
	case selection.VerdictWarning:
		return "Compatible with widened types"
	case selection.VerdictIncompatible:
		if f.Err != "" {
			return f.Err
		}
		return "Incompatible with the selected set"
	default:
		return ""
	}
}

// selTotalSize sums the byte size of every file in a selection snapshot.
func selTotalSize(files []selection.FileState) int64 {
	var n int64
	for _, f := range files {
		n += f.Ref.Size
	}
	return n
}

// rowsLabel renders an estimated row count, or an em dash when unknown (-1).
func rowsLabel(n int64) string {
	if n < 0 {
		return "—"
	}
	return humanCount(n)
}

// hasCompatible reports whether a summary has at least one queryable file.
func hasCompatible(s *selection.CompatSummary) bool {
	return s != nil && s.CompatCount+s.WarnCount > 0
}

// selErrorMessage summarizes the incompatible files for the completion toast.
func selErrorMessage(s *selection.CompatSummary) string {
	if s == nil || s.ErrorCount == 0 {
		return ""
	}
	noun := "file"
	if s.ErrorCount > 1 {
		noun = "files"
	}
	return fmt.Sprintf("%d %s can't be combined — click Details in the selection bar.", s.ErrorCount, noun)
}

// verdictBadge returns the symbol + Tailwind classes for a file's compatibility
// verdict (used by the row badge and the summary).
func verdictBadge(v selection.Verdict) (symbol, classes string) {
	switch v {
	case selection.VerdictCompatible:
		return "✓", "bg-accent-emerald/15 text-accent-emerald"
	case selection.VerdictWarning:
		return "⚠", "bg-accent-amber/15 text-accent-amber"
	case selection.VerdictIncompatible:
		return "✕", "bg-accent-rose/15 text-accent-rose"
	default:
		return "•", "bg-base-700 text-slate-400"
	}
}

// querySelVM assembles the QueryVM for a selection-backed SQL editor: the
// primary virtual table's SQL prefilled, plus the summary for the schema
// preview and options drawer.
func querySelVM(token string, sum *selection.CompatSummary, ready bool) QueryVM {
	vm := QueryVM{Sel: token, SelSummary: sum, SelOpts: sum.Options, Ready: ready}
	if pt := sum.PrimaryTable(); pt != nil {
		vm.InitialSQL = pt.SQL
		vm.Selected = pt.Name
	}
	return vm
}
