// Package api exposes the REST surface of Apexion over chi. All responses are
// JSON. The UI (package ui) is mounted separately by the http server.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/connections"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// API holds the REST handler dependencies.
type API struct {
	catalog     *catalog.Service
	preview     *duckdb.Engine
	explorer    *explorer.Service
	connections *connections.Manager
	store       *storage.Store
	log         zerolog.Logger
}

// New builds the API.
func New(cat *catalog.Service, prev *duckdb.Engine, expl *explorer.Service,
	conns *connections.Manager, log zerolog.Logger) *API {
	return &API{
		catalog: cat, preview: prev, explorer: expl,
		connections: conns, store: cat.Store(), log: log,
	}
}

// Routes returns the mounted REST router.
func (a *API) Routes() http.Handler {
	r := chi.NewRouter()

	r.Get("/buckets", a.listBuckets)
	r.Get("/server/buckets", a.listServerBuckets)
	r.Get("/buckets/{id}", a.getBucket)
	r.Post("/buckets/{name}/crawl", a.crawlBucket)

	r.Get("/datasets", a.listDatasets)
	r.Post("/datasets/crawl", a.crawlDirectory)
	r.Get("/datasets/{id}", a.getDataset)
	r.Post("/datasets/{id}/register", a.registerDataset)
	r.Post("/datasets/{id}/refresh", a.refreshDataset)
	r.Delete("/datasets/{id}", a.deleteDataset)

	r.Get("/catalog", a.listCatalog)
	r.Post("/catalog", a.registerCatalog)
	r.Get("/catalog/{id}", a.getCatalog)
	r.Post("/catalog/{id}/refresh", a.refreshCatalog)
	r.Delete("/catalog/{id}", a.deleteCatalog)
	r.Post("/query", a.runQuery)

	r.Get("/schema", a.getSchema)
	r.Get("/columns", a.getColumns)

	r.Get("/jobs", a.listJobs)
	r.Get("/jobs/{id}", a.getJob)
	r.Post("/jobs/{id}/cancel", a.cancelJob)

	r.Get("/explorer", a.explore)
	r.Post("/directories/crawl", a.crawlDirectory)
	r.Get("/preview/file", a.previewFile)
	r.Get("/preview/dataset/{id}", a.previewDataset)

	r.Get("/connections", a.listConnections)
	r.Post("/connections", a.createConnection)
	r.Post("/connections/test", a.testConnection)
	r.Delete("/connections/{id}", a.deleteConnection)
	r.Post("/connections/{id}/activate", a.activateConnection)

	return r
}

// connectionInput is the create/test payload (secret_key omitted from reads).
type connectionInput struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	UseSSL    bool   `json:"use_ssl"`
	UseRole   bool   `json:"use_role"`
	PathStyle bool   `json:"path_style"`
}

func (in connectionInput) toModel() *model.Connection {
	return &model.Connection{
		Name: in.Name, Provider: in.Provider, Endpoint: in.Endpoint, Region: in.Region,
		AccessKey: in.AccessKey, SecretKey: in.SecretKey, UseSSL: in.UseSSL,
		UseRole: in.UseRole, PathStyle: in.PathStyle,
	}
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := a.connections.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Redact secrets in listings.
	for i := range conns {
		if conns[i].SecretKey != "" {
			conns[i].SecretKey = "••••••••"
		}
	}
	writeJSON(w, http.StatusOK, conns)
}

func (a *API) createConnection(w http.ResponseWriter, r *http.Request) {
	var in connectionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	c := in.toModel()
	if err := a.connections.Create(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.SecretKey = ""
	writeJSON(w, http.StatusCreated, c)
}

func (a *API) testConnection(w http.ResponseWriter, r *http.Request) {
	var in connectionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := a.connections.TestConnection(r.Context(), in.toModel()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := a.connections.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) activateConnection(w http.ResponseWriter, r *http.Request) {
	if err := a.connections.SetActive(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

// ---- handlers ------------------------------------------------------------

func (a *API) listBuckets(w http.ResponseWriter, r *http.Request) {
	bs, err := a.store.ListBuckets(r.Context())
	writeOrErr(w, bs, err)
}

// listServerBuckets returns the buckets that physically exist on the connected
// S3/MinIO server (ListAllMyBuckets), each flagged with whether it has been
// crawled into the catalog.
func (a *API) listServerBuckets(w http.ResponseWriter, r *http.Request) {
	endpoint, names, err := a.catalog.ServerBuckets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"endpoint": endpoint, "error": err.Error(), "buckets": []any{},
		})
		return
	}
	cataloged := map[string]bool{}
	if bs, err := a.store.ListBuckets(r.Context()); err == nil {
		for _, b := range bs {
			cataloged[b.Name] = true
		}
	}
	type serverBucket struct {
		Name      string `json:"name"`
		Cataloged bool   `json:"cataloged"`
	}
	out := make([]serverBucket, 0, len(names))
	for _, n := range names {
		out = append(out, serverBucket{Name: n, Cataloged: cataloged[n]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoint": endpoint, "buckets": out})
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

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	ok := a.catalog.CancelJob(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

func (a *API) explore(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		writeErr(w, http.StatusBadRequest, "bucket required")
		return
	}
	listing, err := a.explorer.ListDir(r.Context(), bucket,
		r.URL.Query().Get("prefix"), r.URL.Query().Get("search"), r.URL.Query().Get("sort"),
		intParam(r, "limit", explorer.DefaultPageSize))
	writeOrErr(w, listing, err)
}

func (a *API) crawlDirectory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bucket string `json:"bucket"`
		Prefix string `json:"prefix"`
		Mode   string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Bucket == "" {
		writeErr(w, http.StatusBadRequest, "bucket required")
		return
	}
	mode := model.CrawlFull
	if body.Mode == "incremental" {
		mode = model.CrawlIncremental
	}
	job := a.catalog.StartCrawlPrefix(body.Bucket, body.Prefix, mode, model.ScheduleManual)
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) previewFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, key := q.Get("bucket"), q.Get("key")
	if bucket == "" || key == "" {
		writeErr(w, http.StatusBadRequest, "bucket and key required")
		return
	}
	format := model.Format(q.Get("format"))
	res, err := a.preview.PreviewFile(r.Context(), bucket, key, format, intParam(r, "limit", 100))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) previewDataset(w http.ResponseWriter, r *http.Request) {
	ds, err := a.store.GetDataset(r.Context(), chi.URLParam(r, "id"))
	if err != nil || ds == nil {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	res, err := a.preview.PreviewDataset(r.Context(), ds.BucketName, ds.Path, ds.Format, intParam(r, "limit", 100))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
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
