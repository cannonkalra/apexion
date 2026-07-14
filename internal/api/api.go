// Package api exposes the REST surface of Apexion over chi. All responses are
// JSON. The UI (package ui) is mounted separately by the http server.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/agents"
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/lineage"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// API holds the REST handler dependencies.
type API struct {
	catalog *catalog.Service
	lineage *lineage.Service
	agents  *agents.Registry
	store   *storage.Store
	log     zerolog.Logger
}

// New builds the API.
func New(cat *catalog.Service, lin *lineage.Service, ag *agents.Registry, log zerolog.Logger) *API {
	return &API{catalog: cat, lineage: lin, agents: ag, store: cat.Store(), log: log}
}

// Routes returns the mounted REST router.
func (a *API) Routes() http.Handler {
	r := chi.NewRouter()

	r.Get("/buckets", a.listBuckets)
	r.Get("/buckets/{id}", a.getBucket)
	r.Post("/buckets/{name}/crawl", a.crawlBucket)

	r.Get("/datasets", a.listDatasets)
	r.Get("/datasets/{id}", a.getDataset)
	r.Delete("/datasets/{id}", a.deleteDataset)
	r.Post("/datasets/{id}/infer", a.inferDataset)

	r.Get("/tables", a.listTables)
	r.Get("/schema", a.getSchema)
	r.Get("/columns", a.getColumns)

	r.Get("/jobs", a.listJobs)
	r.Get("/jobs/{id}", a.getJob)
	r.Get("/runs", a.listRuns)

	r.Get("/lineage", a.getLineage)
	r.Post("/infer", a.inferBody)
	r.Get("/infer/{dataset_id}", a.getInference)

	r.Get("/search", a.search)
	r.Get("/statistics", a.statistics)
	r.Get("/events", a.listEvents)
	r.Get("/agents", a.listAgents)

	return r
}

// ---- handlers ------------------------------------------------------------

func (a *API) listBuckets(w http.ResponseWriter, r *http.Request) {
	bs, err := a.store.ListBuckets(r.Context())
	writeOrErr(w, bs, err)
}

func (a *API) getBucket(w http.ResponseWriter, r *http.Request) {
	b, err := a.store.GetBucket(r.Context(), chi.URLParam(r, "id"))
	writeOrErr(w, b, err)
}

func (a *API) crawlBucket(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	mode := model.CrawlFull
	if r.URL.Query().Get("mode") == "incremental" {
		mode = model.CrawlIncremental
	}
	job := a.catalog.StartCrawl(name, mode, model.ScheduleManual)
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) listDatasets(w http.ResponseWriter, r *http.Request) {
	f := storage.DatasetFilter{
		BucketID: r.URL.Query().Get("bucket_id"),
		Format:   r.URL.Query().Get("format"),
		Search:   r.URL.Query().Get("q"),
		Limit:    intParam(r, "limit", 200),
		Offset:   intParam(r, "offset", 0),
	}
	ds, err := a.store.ListDatasets(r.Context(), f)
	writeOrErr(w, ds, err)
}

func (a *API) getDataset(w http.ResponseWriter, r *http.Request) {
	d, err := a.catalog.DatasetDetail(r.Context(), chi.URLParam(r, "id"))
	if err == nil && d == nil {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	writeOrErr(w, d, err)
}

func (a *API) deleteDataset(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteDataset(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) inferDataset(w http.ResponseWriter, r *http.Request) {
	job, err := a.catalog.StartInference(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) inferBody(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DatasetID string `json:"dataset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DatasetID == "" {
		writeErr(w, http.StatusBadRequest, "dataset_id required")
		return
	}
	job, err := a.catalog.StartInference(body.DatasetID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) getInference(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.LatestInferenceRun(r.Context(), chi.URLParam(r, "dataset_id"))
	writeOrErr(w, run, err)
}

func (a *API) listTables(w http.ResponseWriter, r *http.Request) {
	ds, err := a.store.ListDatasets(r.Context(), storage.DatasetFilter{Limit: 500})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type table struct {
		DatasetID string       `json:"dataset_id"`
		Name      string       `json:"name"`
		Format    model.Format `json:"format"`
		Columns   int          `json:"columns"`
		Version   int          `json:"version"`
	}
	out := make([]table, 0, len(ds))
	for _, d := range ds {
		sc, _ := a.store.LatestSchema(r.Context(), d.ID)
		t := table{DatasetID: d.ID, Name: d.Name, Format: d.Format}
		if sc != nil {
			t.Columns = len(sc.Columns)
			t.Version = sc.Version
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getSchema(w http.ResponseWriter, r *http.Request) {
	datasetID := r.URL.Query().Get("dataset_id")
	if datasetID == "" {
		writeErr(w, http.StatusBadRequest, "dataset_id required")
		return
	}
	sc, err := a.store.LatestSchema(r.Context(), datasetID)
	writeOrErr(w, sc, err)
}

func (a *API) getColumns(w http.ResponseWriter, r *http.Request) {
	if sid := r.URL.Query().Get("schema_id"); sid != "" {
		cols, err := a.store.GetColumns(r.Context(), sid)
		writeOrErr(w, cols, err)
		return
	}
	datasetID := r.URL.Query().Get("dataset_id")
	if datasetID == "" {
		writeErr(w, http.StatusBadRequest, "schema_id or dataset_id required")
		return
	}
	sc, err := a.store.LatestSchema(r.Context(), datasetID)
	if err != nil || sc == nil {
		writeOrErr(w, []model.Column{}, err)
		return
	}
	writeJSON(w, http.StatusOK, sc.Columns)
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.ListJobs(r.Context(), r.URL.Query().Get("status"), intParam(r, "limit", 50))
	writeOrErr(w, jobs, err)
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.store.GetJob(r.Context(), chi.URLParam(r, "id"))
	writeOrErr(w, job, err)
}

func (a *API) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.ListCrawlerRuns(r.Context(), r.URL.Query().Get("bucket_id"), intParam(r, "limit", 50))
	writeOrErr(w, runs, err)
}

func (a *API) getLineage(w http.ResponseWriter, r *http.Request) {
	nodes, edges, err := a.lineage.Graph(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	res, err := a.store.Search(r.Context(), r.URL.Query().Get("q"), intParam(r, "limit", 20))
	writeOrErr(w, res, err)
}

func (a *API) statistics(w http.ResponseWriter, r *http.Request) {
	dash, err := a.store.Dashboard(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	formats, _ := a.store.FormatBreakdown(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"dashboard": dash, "formats": formats})
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	evs, err := a.store.ListEvents(r.Context(), r.URL.Query().Get("type"), intParam(r, "limit", 100))
	writeOrErr(w, evs, err)
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.agents.Info())
}

// ---- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeOrErr(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func intParam(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
