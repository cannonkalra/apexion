// Package ui renders the server-side templ dashboard. It owns the page routes
// (/, /buckets, …) and the HTMX partial/action routes (/ui/*). REST lives in
// package api; both are mounted by the http server.
package ui

import (
	"net/http"
	"sort"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/agents"
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/lineage"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// Handler serves the UI.
type Handler struct {
	catalog *catalog.Service
	lineage *lineage.Service
	agents  *agents.Registry
	store   *storage.Store
	cfg     *config.Config
	log     zerolog.Logger
}

// New builds the UI handler.
func New(cat *catalog.Service, lin *lineage.Service, ag *agents.Registry, cfg *config.Config, log zerolog.Logger) *Handler {
	return &Handler{catalog: cat, lineage: lin, agents: ag, store: cat.Store(), cfg: cfg, log: log}
}

// Routes returns the page + partial router.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.dashboard)
	r.Get("/buckets", h.buckets)
	r.Get("/datasets", h.datasets)
	r.Get("/datasets/{id}", h.datasetDetail)
	r.Get("/schema", h.schema)
	r.Get("/inference", h.inference)
	r.Get("/lineage", h.lineageGraph)
	r.Get("/runs", h.runs)
	r.Get("/search", h.search)
	r.Get("/settings", h.settings)

	r.Route("/ui", func(r chi.Router) {
		r.Get("/partials/jobs", h.partialJobs)
		r.Get("/partials/activity", h.partialActivity)
		r.Get("/search", h.partialSearch)
		r.Post("/buckets/crawl", h.actionCrawlForm)
		r.Post("/buckets/{name}/crawl", h.actionCrawl)
		r.Post("/datasets/{id}/infer", h.actionInfer)
		r.Delete("/datasets/{id}", h.actionDeleteDataset)
		r.Post("/settings/crawl", h.actionSettingsCrawl)
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

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, err := h.store.Dashboard(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	formats, _ := h.store.FormatBreakdown(ctx)
	jobs, _ := h.store.ListJobs(ctx, "", 8)
	runs, _ := h.store.ListCrawlerRuns(ctx, "", 6)
	evs, _ := h.store.ListEvents(ctx, "", 12)
	h.render(w, r, DashboardPage(DashboardVM{Stats: stats, Formats: formats, Jobs: jobs, Runs: runs, Events: evs}))
}

func (h *Handler) buckets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Catalog state: buckets already crawled, keyed by name.
	cataloged, err := h.store.ListBuckets(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	byName := map[string]model.Bucket{}
	for _, b := range cataloged {
		byName[b.Name] = b
	}

	// Live server state: every bucket the credentials can see.
	endpoint, serverNames, serverErr := h.catalog.ServerBuckets(ctx)
	vm := BucketsVM{Endpoint: endpoint, ServerOK: serverErr == nil}
	if serverErr != nil {
		vm.ServerError = serverErr.Error()
		h.log.Warn().Err(serverErr).Msg("list server buckets")
	}

	// Union of server buckets and cataloged buckets, de-duplicated by name.
	seen := map[string]bool{}
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		row := BucketRow{Name: name}
		if b, ok := byName[name]; ok {
			row.Cataloged = true
			row.Bucket = b
			row.Datasets, _ = h.store.ListDatasets(ctx, storage.DatasetFilter{BucketID: b.ID, Limit: 100})
		}
		vm.Rows = append(vm.Rows, row)
	}
	sort.Strings(serverNames)
	for _, n := range serverNames {
		add(n)
	}
	// Cataloged buckets the server didn't return (e.g. ListBuckets restricted).
	for _, b := range cataloged {
		add(b.Name)
	}

	h.render(w, r, BucketsPage(vm))
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
	h.render(w, r, DatasetDetailPage(d))
}

func (h *Handler) schema(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ds, _ := h.store.ListDatasets(ctx, storage.DatasetFilter{Limit: 500})
	vm := SchemaVM{Datasets: ds}
	if id := r.URL.Query().Get("dataset_id"); id != "" {
		vm.Selected, _ = h.catalog.DatasetDetail(ctx, id)
	}
	h.render(w, r, SchemaPage(vm))
}

func (h *Handler) inference(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ds, _ := h.store.ListDatasets(ctx, storage.DatasetFilter{Limit: 500})
	vm := InferenceVM{Datasets: ds}
	if id := r.URL.Query().Get("dataset_id"); id != "" {
		vm.Selected, _ = h.catalog.DatasetDetail(ctx, id)
	}
	h.render(w, r, InferencePage(vm))
}

func (h *Handler) lineageGraph(w http.ResponseWriter, r *http.Request) {
	nodes, edges, err := h.lineage.Graph(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, LineagePage(LineageVM{Nodes: nodes, Edges: edges, Groups: groupLineage(nodes)}))
}

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	runs, err := h.store.ListCrawlerRuns(r.Context(), "", 100)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, RunsPage(RunsVM{Runs: runs}))
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	h.render(w, r, SearchPage(h.buildSearch(r, q)))
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	bs, _ := h.store.ListBuckets(r.Context())
	agentsVM := make([]AgentInfoVM, 0)
	for _, a := range h.agents.Info() {
		agentsVM = append(agentsVM, AgentInfoVM{Name: a.Name, Kind: a.Kind, Provider: a.Provider, Enabled: a.Enabled})
	}
	vm := SettingsVM{
		MinIOEndpoint: h.cfg.MinIO.Endpoint,
		MinIORegion:   h.cfg.MinIO.Region,
		StoragePath:   h.cfg.Storage.Path,
		Workers:       h.cfg.Crawler.Workers,
		AgentProvider: h.cfg.Agents.Provider,
		AgentModel:    h.cfg.Agents.LLM.Model,
		Agents:        agentsVM,
		Buckets:       bs,
	}
	h.render(w, r, SettingsPage(vm))
}

// ---- partials ------------------------------------------------------------

func (h *Handler) partialJobs(w http.ResponseWriter, r *http.Request) {
	jobs, _ := h.store.ListJobs(r.Context(), "", 8)
	h.render(w, r, JobsList(jobs))
}

func (h *Handler) partialActivity(w http.ResponseWriter, r *http.Request) {
	evs, _ := h.store.ListEvents(r.Context(), "", 12)
	h.render(w, r, ActivityFeed(evs))
}

func (h *Handler) partialSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.render(w, r, SearchResultsPartial(h.buildSearch(r, q)))
}

func (h *Handler) buildSearch(r *http.Request, q string) SearchVM {
	vm := SearchVM{Query: q}
	if q == "" {
		return vm
	}
	if res, err := h.store.Search(r.Context(), q, 15); err == nil {
		vm.Datasets = res.Datasets
		vm.Columns = res.Columns
		vm.Buckets = res.Buckets
	}
	return vm
}

// ---- actions -------------------------------------------------------------

func (h *Handler) actionCrawl(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.catalog.StartCrawl(name, model.CrawlFull, model.ScheduleManual)
	h.render(w, r, Toast("Crawl started for "+name, "success"))
}

func (h *Handler) actionCrawlForm(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("bucket")
	if name == "" {
		h.render(w, r, Toast("Bucket name is required", "error"))
		return
	}
	h.catalog.StartCrawl(name, model.CrawlFull, model.ScheduleManual)
	w.Header().Set("HX-Redirect", "/buckets")
	h.render(w, r, Toast("Crawl started for "+name, "success"))
}

func (h *Handler) actionInfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.catalog.StartInference(id); err != nil {
		h.render(w, r, Toast("Inference failed: "+err.Error(), "error"))
		return
	}
	h.render(w, r, Toast("Inference started", "success"))
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

// ---- helpers -------------------------------------------------------------

func (h *Handler) fail(w http.ResponseWriter, err error) {
	h.log.Error().Err(err).Msg("ui handler error")
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// groupLineage buckets nodes by kind in the display order.
func groupLineage(nodes []model.LineageNode) []lineageGroup {
	order := []struct{ kind, label string }{
		{"bucket", "Buckets"}, {"dataset", "Datasets"}, {"table", "Tables"},
		{"column", "Columns"}, {"partition", "Partitions"},
	}
	byKind := map[string][]model.LineageNode{}
	for _, n := range nodes {
		byKind[n.Kind] = append(byKind[n.Kind], n)
	}
	var groups []lineageGroup
	for _, o := range order {
		ns := byKind[o.kind]
		if len(ns) == 0 {
			continue
		}
		sort.Slice(ns, func(i, j int) bool { return ns[i].Label < ns[j].Label })
		groups = append(groups, lineageGroup{Kind: o.kind, Label: o.label, Nodes: ns})
	}
	return groups
}
