package ui

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
)

// catalogPage lists the registered logical SQL tables.
func (h *Handler) catalogPage(w http.ResponseWriter, r *http.Request) {
	entries, err := h.catalog.ListCatalog(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, CatalogPage(CatalogVM{Entries: entries, Lake: h.preview.LakeInfo(r.Context())}))
}

// queryPage renders the SQL query console over the catalog tables. With ?sel=
// it prefills a multi-file selection as a virtual table (options drawer + schema
// preview); with ?table= it prefills a single catalog table.
func (h *Handler) queryPage(w http.ResponseWriter, r *http.Request) {
	entries, _ := h.catalog.ListCatalog(r.Context())
	vm := QueryVM{Tables: entries, LakeOther: h.lakeOther(r.Context(), entries), Ready: h.preview.Ready()}
	if token := r.URL.Query().Get("sel"); token != "" {
		snap, ok := h.selection.Progress(token)
		switch {
		case !ok:
			vm.Sel, vm.SelExpired = token, true
		case snap.Complete && snap.Summary != nil:
			vm = querySelVM(token, snap.Summary, h.preview.Ready())
			vm.Tables = entries
		default:
			// Still discovering — show the editor shell; the user can retry.
			vm.Sel, vm.SelExpired = token, true
		}
		h.render(w, r, QueryPage(vm))
		return
	}
	// Single-file editor: "Query in SQL" from a file preview passes the file's
	// identity + reader options. The editor prefills a SELECT over the file and
	// shows a reader-options bar (format override + toggles) to adjust it.
	if r.URL.Query().Get("bucket") != "" && r.URL.Query().Get("key") != "" {
		f := parseFileQuery(r)
		vm.File = &f
		vm.InitialSQL, _ = fileInitialSQL(f.Bucket, f.Key, f.Format, f.Opts)
		h.render(w, r, QueryPage(vm))
		return
	}
	vm.InitialSQL, vm.Selected = queryInitialSQL(r.URL.Query().Get("table"), r.URL.Query().Get("sql"))
	h.render(w, r, QueryPage(vm))
}

// lakeOther returns the DuckLake objects that are not catalog entries, i.e.
// tables and views other clients created in the shared lake.
func (h *Handler) lakeOther(ctx context.Context, entries []model.CatalogEntry) []duckdb.LakeObject {
	objs, err := h.preview.LakeObjects(ctx)
	if err != nil {
		h.log.Debug().Err(err).Msg("list DuckLake objects")
		return nil
	}
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[e.Name] = true
	}
	var out []duckdb.LakeObject
	for _, o := range objs {
		if o.Schema == "main" && known[o.Name] {
			continue
		}
		out = append(out, o)
	}
	return out
}

// lakeObjectRef is the SQL reference for a lake object from the default
// catalog: a bare name for a plain identifier in main, otherwise quoted and
// schema-qualified.
func lakeObjectRef(o duckdb.LakeObject) string {
	if o.Schema == "main" && duckdb.ValidIdentifier(o.Name) {
		return o.Name
	}
	q := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	return q(o.Schema) + "." + q(o.Name)
}

// queryInitialSQL derives the editor's prefilled SQL and the selected sidebar
// table from the query-page params. A ?table= (a registered catalog table)
// wins: it both prefills a SELECT and highlights the sidebar entry. Otherwise a
// ?sql= statement — the "Query in SQL" entry point from the file-preview page,
// which carries a SELECT over the file's reader expression — is used verbatim
// with no sidebar selection (an ad-hoc file is not a catalog table).
func queryInitialSQL(table, sql string) (initialSQL, selected string) {
	if table != "" {
		return "SELECT * FROM " + table + " LIMIT 100", table
	}
	return sql, ""
}

// actionRegisterCatalog registers a dataset as a catalog table (HTMX form).
func (h *Handler) actionRegisterCatalog(w http.ResponseWriter, r *http.Request) {
	datasetID := r.FormValue("dataset_id")
	name := r.FormValue("name")
	if datasetID == "" {
		h.render(w, r, Toast("Dataset is required", "error"))
		return
	}
	entry, err := h.catalog.RegisterDataset(r.Context(), datasetID, name)
	if err != nil {
		h.render(w, r, Toast("Register failed: "+err.Error(), "error"))
		return
	}
	w.Header().Set("HX-Redirect", "/query?table="+entry.Name)
	h.render(w, r, Toast("Registered table "+entry.Name, "success"))
}

// actionRegisterDataset registers a dataset as a table with a derived name — the
// one-click "Register" from the Datasets list / dataset detail.
func (h *Handler) actionRegisterDataset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	entry, err := h.catalog.RegisterDataset(r.Context(), id, r.FormValue("name"))
	if err != nil {
		h.render(w, r, Toast("Register failed: "+err.Error(), "error"))
		return
	}
	w.Header().Set("HX-Redirect", "/query?table="+entry.Name)
	h.render(w, r, Toast("Registered table "+entry.Name, "success"))
}

// actionRefreshDataset triggers an incremental re-crawl of a dataset's directory
// (and refreshes any views over it via their own refresh).
func (h *Handler) actionRefreshDataset(w http.ResponseWriter, r *http.Request) {
	ds, err := h.store.GetDataset(r.Context(), chi.URLParam(r, "id"))
	if err != nil || ds == nil {
		h.render(w, r, Toast("Dataset not found", "error"))
		return
	}
	h.catalog.StartCrawlPrefix(ds.BucketName, ds.Path, model.CrawlIncremental, model.ScheduleManual)
	h.render(w, r, Toast("Refresh started for "+ds.Name, "success"))
}

// actionRunQuery executes a read-only SQL query and returns the result table.
func (h *Handler) actionRunQuery(w http.ResponseWriter, r *http.Request) {
	sql := r.FormValue("sql")
	limit := intFrom(r.FormValue("limit"), 200)
	res, err := h.catalog.Query(r.Context(), sql, limit)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, ResultTable(res))
}

// actionRefreshCatalog rebuilds a table's view and re-crawls its dataset.
func (h *Handler) actionRefreshCatalog(w http.ResponseWriter, r *http.Request) {
	if err := h.catalog.RefreshCatalog(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.render(w, r, Toast("Refresh failed: "+err.Error(), "error"))
		return
	}
	h.render(w, r, Toast("Table refreshed", "success"))
}

// actionDeleteCatalog removes a table (row-removing empty response).
func (h *Handler) actionDeleteCatalog(w http.ResponseWriter, r *http.Request) {
	if err := h.catalog.DeleteCatalog(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.render(w, r, Toast("Delete failed: "+err.Error(), "error"))
		return
	}
	w.WriteHeader(http.StatusOK)
}
