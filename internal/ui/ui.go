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
	"github.com/apexion/apexion/internal/catalog/virtualpath"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/connections"
	"github.com/apexion/apexion/internal/dataviewer"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection"
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
	profiler    *dataviewer.Profiler
	explorer    *explorer.Service
	connections *connections.Manager
	selection   *selection.SelectionService
	store       *storage.Store
	cfg         *config.Config
	log         zerolog.Logger
}

// New builds the UI handler.
func New(cat *catalog.Service, prev *duckdb.Engine, expl *explorer.Service,
	conns *connections.Manager, sel *selection.SelectionService, cfg *config.Config, log zerolog.Logger) *Handler {
	return &Handler{
		catalog: cat, preview: prev, profiler: dataviewer.New(prev, log),
		explorer: expl, connections: conns,
		selection: sel, store: cat.Store(), cfg: cfg, log: log,
	}
}

// Routes returns the page + partial router.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/datasets", http.StatusFound)
	})
	r.Get("/explorer", h.explorerPage)
	r.Get("/datasets", h.datasets)
	r.Get("/datasets/{id}", h.datasetDetail)
	r.Get("/preview", h.filePreview)
	r.Get("/catalog", h.catalogPage)
	r.Get("/query", h.queryPage)
	r.Get("/wizard", h.wizardPage)
	r.Get("/settings", h.settings)

	r.Route("/ui", func(r chi.Router) {
		r.Get("/partials/explorer-summary", h.partialExplorerSummary)
		r.Get("/partials/explorer-page", h.partialExplorerPage)
		r.Get("/preview", h.partialFilePreview)
		r.Get("/insights", h.partialColumnInsights)
		r.Get("/datasets/{id}/preview", h.partialDatasetPreview)
		r.Post("/datasets/{id}/register", h.actionRegisterDataset)
		r.Post("/datasets/{id}/refresh", h.actionRefreshDataset)
		r.Post("/directories/crawl", h.actionCrawlDirectory)
		r.Post("/wizard/crawl", h.wizardCrawl)
		r.Get("/wizard/progress", h.wizardProgress)
		r.Post("/wizard/register-form", h.wizardRegisterForm)
		r.Post("/wizard/register", h.wizardRegister)
		r.Post("/wizard/skip", h.wizardSkip)
		r.Delete("/datasets/{id}", h.actionDeleteDataset)
		r.Post("/catalog", h.actionRegisterCatalog)
		r.Post("/catalog/{id}/refresh", h.actionRefreshCatalog)
		r.Delete("/catalog/{id}", h.actionDeleteCatalog)
		r.Post("/query", h.actionRunQuery)
		// Multi-file smart selection (background schema discovery → virtual table).
		r.Post("/selection", h.actionSelectionSet)
		r.Get("/selection/progress", h.partialSelectionProgress)
		r.Post("/selection/regenerate", h.actionSelectionRegenerate)
		r.Post("/selection/options", h.actionSelectionOptions)
		r.Get("/selection/preview", h.partialSelectionPreview)
		r.Post("/selection/save", h.actionSelectionSave)
		r.Post("/selection/clear", h.actionSelectionClear)
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

	// Cap the rendered bucket rail so a large account doesn't dump thousands of
	// rows into the initial DOM. The full set is assembled above; ExplorerVM.
	// TotalBuckets records the true count so the sidebar can show a "Load more
	// buckets" control. The cap is raised via the ?buckets= query param.
	total := len(buckets)
	bucketCap := intFrom(r.URL.Query().Get("buckets"), explorer.DefaultBucketPageSize)
	if bucketCap < len(buckets) {
		buckets = buckets[:bucketCap]
	}

	conns, _ := h.connections.List(ctx)
	vm := ExplorerVM{
		Buckets: buckets, Bucket: selected,
		Connections: conns, ActiveConn: h.connections.Active(),
		ServerListOK: serverErr == nil,
		Infinite:     r.URL.Query().Get("infinite") == "1",
		TotalBuckets: total,
	}
	if selected != "" {
		prefix := r.URL.Query().Get("prefix")
		search := r.URL.Query().Get("search")
		sortBy := r.URL.Query().Get("sort")
		cursor := r.URL.Query().Get("cursor")
		limit := intFrom(r.URL.Query().Get("limit"), explorer.DefaultPageSize)
		listing, err := h.explorer.ListDir(ctx, selected, prefix, search, sortBy, cursor, limit)
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

// partialExplorerPage returns ONE cursor-resumed page of a folder: just the rows
// to append plus a fresh #load-more sentinel carrying the next cursor. HTMX
// swaps it (outerHTML) onto the current sentinel, so only new rows are added and
// existing rows/scroll are untouched.
func (h *Handler) partialExplorerPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket := q.Get("bucket")
	if bucket == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	limit := intFrom(q.Get("limit"), explorer.DefaultPageSize)
	listing, err := h.explorer.ListDir(r.Context(), bucket, q.Get("prefix"), q.Get("search"), q.Get("sort"), q.Get("cursor"), limit)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, explorerPageFragment(listing, q.Get("infinite") == "1"))
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
	opts := model.DefaultReadOptions()
	opts.Filename = false
	vm := PreviewVM{
		Bucket: bucket, Key: key, Name: baseName(key), Format: f,
		Compression: format.DetectCompression(key), Limit: 100, Ready: h.preview.Ready(),
		Opts: opts,
	}
	// Show how this object maps to a virtual Hive-partitioned path (parent
	// directories → pt0, pt1, …) without touching the stored object.
	vp := virtualpath.New(h.cfg.Catalog.VirtualPartitionPrefix, h.cfg.Catalog.VirtualPartitionSeparator).Build(key, 1<<30)
	vm.Partitions = vp.Partitions
	vm.VirtualPath = vp.Virtual
	res, err := h.preview.PreviewFileWith(r.Context(), bucket, key, f, opts, 100)
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
	counts, _ := h.catalog.RegistrationCounts(ctx)
	h.render(w, r, DatasetsPage(DatasetsVM{Datasets: ds, Buckets: bs, Filter: f, Registered: counts}))
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
	status, _ := h.catalog.DatasetCatalogStatus(r.Context(), d.Dataset.ID)
	suggested := h.catalog.SuggestTableName(r.Context(), d.Dataset.Name)
	h.render(w, r, DatasetDetailPage(d, tab, status, suggested))
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
	partNames := h.catalog.VirtualPartitionColumns(r.Context(), ds)
	res, err := h.preview.PreviewDatasetPartitioned(r.Context(), ds.BucketName, ds.Path, ds.Format,
		h.store.SampleObjectKey(r.Context(), ds), partNames, previewOptsFromRequest(r), 100)
	if err != nil {
		h.render(w, r, QueryError(err.Error()))
		return
	}
	h.render(w, r, PreviewResult(res))
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
	// IAM-first: the form's Authentication select drives whether static keys are
	// used. "iam" uses the AWS credential chain (instance profile / ECS / IRSA,
	// env, ~/.aws) — no keys needed. A missing "auth" field falls back to the
	// legacy use_role checkbox so older clients keep working.
	auth := r.FormValue("auth")
	useRole := auth == "iam" || (auth == "" && checked("use_role"))
	c := &model.Connection{
		Name:         r.FormValue("name"),
		Provider:     r.FormValue("provider"),
		Endpoint:     r.FormValue("endpoint"),
		Region:       r.FormValue("region"),
		AccessKey:    r.FormValue("access_key"),
		SecretKey:    r.FormValue("secret_key"),
		SessionToken: r.FormValue("session_token"),
		UseSSL:       checked("use_ssl"),
		UseRole:      useRole,
		PathStyle:    checked("path_style"),
	}
	if c.Name == "" {
		h.render(w, r, Toast("Connection name is required", "error"))
		return
	}
	// Credentials are only required for explicit key-based auth. IAM connections
	// intentionally leave them blank.
	if !useRole && (c.AccessKey == "" || c.SecretKey == "") {
		h.render(w, r, Toast("Access key and secret key are required for key-based authentication", "error"))
		return
	}
	// Never persist stray key material for an IAM connection.
	if useRole {
		c.AccessKey = ""
		c.SecretKey = ""
		c.SessionToken = ""
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
