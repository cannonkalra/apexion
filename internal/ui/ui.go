// Package ui renders the server-side templ dashboard. It owns the page routes
// (/explorer, /datasets, /settings) and the HTMX partial/action routes (/ui/*).
// REST lives in package api; both are mounted by the http server.
package ui

import (
	"net/http"
	"path"
	"sort"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/connections"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

func baseName(key string) string { return path.Base(key) }

func intFrom(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

// Handler serves the UI.
type Handler struct {
	catalog     *catalog.Service
	preview     *duckdb.Engine
	explorer    *explorer.Service
	connections *connections.Manager
	store       *storage.Store
	cfg         *config.Config
	log         zerolog.Logger
}

// New builds the UI handler.
func New(cat *catalog.Service, prev *duckdb.Engine, expl *explorer.Service,
	conns *connections.Manager, cfg *config.Config, log zerolog.Logger) *Handler {
	return &Handler{
		catalog: cat, preview: prev, explorer: expl, connections: conns,
		store: cat.Store(), cfg: cfg, log: log,
	}
}

// Routes returns the page + partial router.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/explorer", http.StatusFound)
	})
	r.Get("/explorer", h.explorerPage)
	r.Get("/datasets", h.datasets)
	r.Get("/datasets/{id}", h.datasetDetail)
	r.Get("/preview", h.filePreview)
	r.Get("/catalog", h.catalogPage)
	r.Get("/query", h.queryPage)
	r.Get("/settings", h.settings)

	r.Route("/ui", func(r chi.Router) {
		r.Get("/partials/explorer-summary", h.partialExplorerSummary)
		r.Get("/datasets/{id}/preview", h.partialDatasetPreview)
		r.Post("/directories/crawl", h.actionCrawlDirectory)
		r.Delete("/datasets/{id}", h.actionDeleteDataset)
		r.Post("/catalog", h.actionRegisterCatalog)
		r.Post("/catalog/{id}/refresh", h.actionRefreshCatalog)
		r.Delete("/catalog/{id}", h.actionDeleteCatalog)
		r.Post("/query", h.actionRunQuery)
		r.Post("/settings/crawl", h.actionSettingsCrawl)
		r.Post("/connections", h.actionCreateConnection)
		r.Post("/connections/{id}/activate", h.actionActivateConnection)
		r.Delete("/connections/{id}", h.actionDeleteConnection)
	})
	return r
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		h.log.Error().Err(err).Msg("render")
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// ---- pages ---------------------------------------------------------------

func (h *Handler) explorerPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	selected := r.URL.Query().Get("bucket")

	// ListAllMyBuckets may be denied by the credentials. Build the bucket rail
	// from every source we have: the server listing (best-effort), buckets we've
	// already cataloged/connected, and whatever bucket the user is browsing.
	_, serverNames, serverErr := h.catalog.ServerBuckets(ctx)
	cataloged, _ := h.store.ListBuckets(ctx)

	seen := map[string]bool{}
	var buckets []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			buckets = append(buckets, n)
		}
	}
	for _, n := range serverNames {
		add(n)
	}
	for _, b := range cataloged {
		add(b.Name)
	}
	add(selected)
	sort.Strings(buckets)

	conns, _ := h.connections.List(ctx)
	vm := ExplorerVM{
		Buckets: buckets, Bucket: selected,
		Connections: conns, ActiveConn: h.connections.Active(),
		ServerListOK: serverErr == nil,
	}
	if selected != "" {
		prefix := r.URL.Query().Get("prefix")
		search := r.URL.Query().Get("search")
		sortBy := r.URL.Query().Get("sort")
		limit := intFrom(r.URL.Query().Get("limit"), explorer.DefaultPageSize)
		listing, err := h.explorer.ListDir(ctx, selected, prefix, search, sortBy, limit)
		if err != nil {
			vm.Error = err.Error()
		} else {
			vm.Listing = listing
			vm.Crumbs = explorer.Breadcrumbs(prefix)
		}
	}
	h.render(w, r, ExplorerPage(vm))
}

// partialExplorerSummary lazily computes a folder's recursive summary so the
// directory listing renders instantly.
func (h *Handler) partialExplorerSummary(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	summary, err := h.explorer.FolderSummary(r.Context(), bucket, r.URL.Query().Get("prefix"))
	if err != nil {
		h.render(w, r, FolderStatsError())
		return
	}
	h.render(w, r, FolderStats(*summary))
}

func (h *Handler) filePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, key := q.Get("bucket"), q.Get("key")
	if bucket == "" || key == "" {
		http.Error(w, "bucket and key required", http.StatusBadRequest)
		return
	}
	f := model.Format(q.Get("format"))
	if f == "" {
		f = format.DetectFormat(key, nil)
	}
	vm := PreviewVM{
		Bucket: bucket, Key: key, Name: baseName(key), Format: f,
		Compression: format.DetectCompression(key), Limit: 100, Ready: h.preview.Ready(),
	}
	res, err := h.preview.PreviewFile(r.Context(), bucket, key, f, 100)
	if err != nil {
		vm.Error = err.Error()
	} else {
		vm.Result = res
	}
	h.render(w, r, FilePreviewPage(vm))
}

func (h *Handler) datasets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := storage.DatasetFilter{
		BucketID: r.URL.Query().Get("bucket_id"),
		Format:   r.URL.Query().Get("format"),
		Search:   r.URL.Query().Get("q"),
		Limit:    300,
	}
	ds, err := h.store.ListDatasets(ctx, f)
	if err != nil {
		h.fail(w, err)
		return
	}
	bs, _ := h.store.ListBuckets(ctx)
	h.render(w, r, DatasetsPage(DatasetsVM{Datasets: ds, Buckets: bs, Filter: f}))
}

func (h *Handler) datasetDetail(w http.ResponseWriter, r *http.Request) {
	d, err := h.catalog.DatasetDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if d == nil {
		http.NotFound(w, r)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}
	h.render(w, r, DatasetDetailPage(d, tab))
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bs, _ := h.store.ListBuckets(ctx)
	conns, _ := h.connections.List(ctx)
	active := ""
	if a := h.connections.Active(); a != nil {
		active = a.ID
	}
	vm := SettingsVM{
		Connections:  conns,
		ActiveConnID: active,
		StoragePath:  h.cfg.Storage.Path,
		Workers:      h.cfg.Crawler.Workers,
		Buckets:      bs,
	}
	h.render(w, r, SettingsPage(vm))
}

// ---- partials ------------------------------------------------------------

func (h *Handler) partialDatasetPreview(w http.ResponseWriter, r *http.Request) {
	ds, err := h.store.GetDataset(r.Context(), chi.URLParam(r, "id"))
	if err != nil || ds == nil {
		h.render(w, r, QueryError("dataset not found"))
		return
	}
	res, err := h.preview.PreviewDataset(r.Context(), ds.BucketName, ds.Path, ds.Format, 100)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, ResultTable(res))
}

// ---- actions -------------------------------------------------------------

func (h *Handler) actionCrawlDirectory(w http.ResponseWriter, r *http.Request) {
	bucket := r.FormValue("bucket")
	prefix := r.FormValue("prefix")
	if bucket == "" {
		h.render(w, r, Toast("Bucket is required", "error"))
		return
	}
	h.catalog.StartCrawlPrefix(bucket, prefix, model.CrawlFull, model.ScheduleManual)
	label := bucket
	if prefix != "" {
		label = bucket + "/" + prefix
	}
	h.render(w, r, Toast("Crawl started for "+label, "success"))
}

func (h *Handler) actionDeleteDataset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteDataset(r.Context(), id); err != nil {
		h.render(w, r, Toast("Delete failed: "+err.Error(), "error"))
		return
	}
	// Empty body removes the target row (hx-swap outerHTML).
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) actionSettingsCrawl(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("bucket")
	if name == "" {
		h.render(w, r, Toast("Bucket name is required", "error"))
		return
	}
	mode := model.CrawlFull
	if r.FormValue("mode") == "incremental" {
		mode = model.CrawlIncremental
	}
	h.catalog.StartCrawl(name, mode, model.ScheduleManual)
	h.render(w, r, Toast("Crawl started for "+name, "success"))
}

func (h *Handler) actionCreateConnection(w http.ResponseWriter, r *http.Request) {
	checked := func(name string) bool {
		v := r.FormValue(name)
		return v == "on" || v == "true"
	}
	c := &model.Connection{
		Name:      r.FormValue("name"),
		Provider:  r.FormValue("provider"),
		Endpoint:  r.FormValue("endpoint"),
		Region:    r.FormValue("region"),
		AccessKey: r.FormValue("access_key"),
		SecretKey: r.FormValue("secret_key"),
		UseSSL:    checked("use_ssl"),
		UseRole:   checked("use_role"),
		PathStyle: checked("path_style"),
	}
	if c.Name == "" {
		h.render(w, r, Toast("Connection name is required", "error"))
		return
	}
	if err := h.connections.Create(r.Context(), c); err != nil {
		h.render(w, r, Toast("Failed to add connection: "+err.Error(), "error"))
		return
	}
	w.Header().Set("HX-Redirect", "/settings")
	h.render(w, r, Toast("Connection added", "success"))
}

func (h *Handler) actionActivateConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.connections.SetActive(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.render(w, r, Toast("Failed to switch: "+err.Error(), "error"))
		return
	}
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/settings"
	}
	w.Header().Set("HX-Redirect", redirect)
	h.render(w, r, Toast("Active connection switched", "success"))
}

func (h *Handler) actionDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.connections.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.render(w, r, Toast("Failed to delete: "+err.Error(), "error"))
		return
	}
	w.Header().Set("HX-Redirect", "/settings")
	h.render(w, r, Toast("Connection deleted", "success"))
}

// ---- helpers -------------------------------------------------------------

func (h *Handler) fail(w http.ResponseWriter, err error) {
	h.log.Error().Err(err).Msg("ui handler error")
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
