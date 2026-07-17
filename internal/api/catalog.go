package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/model"
)

// listCatalog returns every registered logical table.
func (a *API) listCatalog(w http.ResponseWriter, r *http.Request) {
	entries, err := a.catalog.ListCatalog(r.Context())
	writeOrErr(w, entries, err)
}

// getCatalog returns one catalog entry.
func (a *API) getCatalog(w http.ResponseWriter, r *http.Request) {
	e, err := a.catalog.GetCatalog(r.Context(), chi.URLParam(r, "id"))
	if err == nil && e == nil {
		writeErr(w, http.StatusNotFound, "catalog entry not found")
		return
	}
	writeOrErr(w, e, err)
}

// registerCatalog registers a dataset as a logical SQL table.
func (a *API) registerCatalog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DatasetID string `json:"dataset_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DatasetID == "" {
		writeErr(w, http.StatusBadRequest, "dataset_id required")
		return
	}
	entry, err := a.catalog.RegisterDataset(r.Context(), body.DatasetID, body.Name)
	switch {
	case errors.Is(err, catalog.ErrDatasetNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, catalog.ErrTableNameTaken):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusCreated, entry)
	}
}

// registerDataset registers a dataset (by id) as a catalog table — the
// workflow-oriented entry point (the crawl wizard's final step calls this).
func (a *API) registerDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		SchemaStrategy string `json:"schema_strategy"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // all fields optional
	entry, err := a.catalog.RegisterDatasetWith(r.Context(), chi.URLParam(r, "id"), catalog.RegisterOptions{
		Name: body.Name, Description: body.Description, SchemaStrategy: body.SchemaStrategy,
	})
	switch {
	case errors.Is(err, catalog.ErrDatasetNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, catalog.ErrTableNameTaken):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusCreated, entry)
	}
}

// refreshCatalog rebuilds a table's view and re-crawls its dataset.
func (a *API) refreshCatalog(w http.ResponseWriter, r *http.Request) {
	if err := a.catalog.RefreshCatalog(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

// deleteCatalog removes a table and drops its view.
func (a *API) deleteCatalog(w http.ResponseWriter, r *http.Request) {
	if err := a.catalog.DeleteCatalog(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// runQuery executes a read-only SQL statement against the catalog views.
func (a *API) runQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SQL == "" {
		writeErr(w, http.StatusBadRequest, "sql required")
		return
	}
	res, err := a.catalog.Query(r.Context(), body.SQL, body.Limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// refreshDataset triggers an incremental re-crawl of a dataset's directory.
func (a *API) refreshDataset(w http.ResponseWriter, r *http.Request) {
	ds, err := a.store.GetDataset(r.Context(), chi.URLParam(r, "id"))
	if err != nil || ds == nil {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	job := a.catalog.StartCrawlPrefix(ds.BucketName, ds.Path, model.CrawlIncremental, model.ScheduleManual)
	writeJSON(w, http.StatusAccepted, job)
}
