package ui

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/apexion/apexion/internal/model"
)

// catalogPage lists the registered logical SQL tables.
func (h *Handler) catalogPage(w http.ResponseWriter, r *http.Request) {
	entries, err := h.catalog.ListCatalog(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, CatalogPage(CatalogVM{Entries: entries}))
}

// queryPage renders the SQL query console over the catalog tables.
func (h *Handler) queryPage(w http.ResponseWriter, r *http.Request) {
	entries, _ := h.catalog.ListCatalog(r.Context())
	vm := QueryVM{Tables: entries, Ready: h.preview.Ready()}
	if t := r.URL.Query().Get("table"); t != "" {
		vm.InitialSQL = "SELECT * FROM " + t + " LIMIT 100"
		vm.Selected = t
	}
	h.render(w, r, QueryPage(vm))
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
