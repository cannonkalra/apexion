// Package app wires every subsystem together (dependency injection) and owns
// the process lifecycle: startup, serving, and graceful shutdown.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/agents"
	"github.com/apexion/apexion/internal/api"
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/crawler"
	"github.com/apexion/apexion/internal/crawler/s3"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/httpserver"
	"github.com/apexion/apexion/internal/inference"
	"github.com/apexion/apexion/internal/jobs"
	"github.com/apexion/apexion/internal/lineage"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
	"github.com/apexion/apexion/internal/ui"
	"github.com/apexion/apexion/pkg/logger"
)

// App holds every wired dependency and the process lifecycle handles.
type App struct {
	Cfg       *config.Config
	Log       zerolog.Logger
	Store     *storage.Store
	Bus       *events.Bus
	S3        *s3.Client
	Crawler   *crawler.Crawler
	Catalog   *catalog.Service
	Lineage   *lineage.Service
	Jobs      *jobs.Manager
	Scheduler *jobs.Scheduler
	Agents    *agents.Registry
	Server    *httpserver.Server
}

// Build constructs the full application graph from configuration.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	log := logger.New(cfg.Log.Level, cfg.Log.Pretty)

	store, err := storage.Open(cfg.Storage.Path, cfg.Storage.MaxOpenConns, log)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	bus := events.NewBus(log, 4096)

	s3client, err := s3.New(s3.Config{
		Endpoint:  cfg.MinIO.Endpoint,
		AccessKey: cfg.MinIO.AccessKey,
		SecretKey: cfg.MinIO.SecretKey,
		UseSSL:    cfg.MinIO.UseSSL,
		Region:    cfg.MinIO.Region,
	})
	if err != nil {
		return nil, err
	}

	registry := crawler.DefaultRegistry()
	resolvers := crawler.DefaultResolvers()
	cr := crawler.New(store, s3client, registry, resolvers, bus, cfg.Crawler, log)

	engine := inference.NewEngine()
	jobMgr := jobs.NewManager(store, log, cfg.Crawler.Workers)

	cat := catalog.New(store, s3client, cr, engine, jobMgr, bus, cfg.Inference, log)
	cat.PersistEvents(bus)

	lin := lineage.New(store, log)
	lin.Subscribe(bus)

	provider := buildProvider(cfg)
	agentReg := agents.NewRegistry(provider, store, bus, log)

	sched := jobs.NewScheduler(store, cat, log)

	apiH := api.New(cat, lin, agentReg, log)
	uiH := ui.New(cat, lin, agentReg, cfg, log)
	srv := httpserver.New(cfg.Server, apiH, uiH, log)

	return &App{
		Cfg: cfg, Log: log, Store: store, Bus: bus, S3: s3client, Crawler: cr,
		Catalog: cat, Lineage: lin, Jobs: jobMgr, Scheduler: sched,
		Agents: agentReg, Server: srv,
	}, nil
}

func buildProvider(cfg *config.Config) agents.Provider {
	if !cfg.Agents.Enabled {
		return agents.NewProvider(agents.Config{Provider: "noop"})
	}
	return agents.NewProvider(agents.Config{
		Provider: cfg.Agents.Provider,
		BaseURL:  cfg.Agents.LLM.BaseURL,
		APIKey:   cfg.Agents.LLM.APIKey,
		Model:    cfg.Agents.LLM.Model,
	})
}

// Serve starts the scheduler and HTTP server and blocks until the context is
// cancelled, then shuts everything down gracefully.
func (a *App) Serve(ctx context.Context) error {
	if err := a.Scheduler.Start(ctx); err != nil {
		a.Log.Warn().Err(err).Msg("scheduler start")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- a.Server.Start() }()

	a.Log.Info().Str("ui", "http://"+a.Cfg.Server.Addr()).Msg("Apexion is ready")

	select {
	case <-ctx.Done():
		a.Log.Info().Msg("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return a.Shutdown()
}

// Shutdown drains the server, scheduler, jobs, event bus, and store.
func (a *App) Shutdown() error {
	shutCtx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := a.Server.Shutdown(shutCtx); err != nil {
		a.Log.Warn().Err(err).Msg("server shutdown")
	}
	a.Scheduler.Stop()
	a.Jobs.Shutdown(shutCtx)
	a.Bus.Close()
	if err := a.Store.Close(); err != nil {
		a.Log.Warn().Err(err).Msg("store close")
	}
	a.Log.Info().Msg("shutdown complete")
	return nil
}

// CrawlOnce runs a single synchronous crawl (used by the CLI `crawl` command).
func (a *App) CrawlOnce(ctx context.Context, bucket, mode string) error {
	m := parseCrawlMode(mode)
	start := time.Now()
	run, err := a.Crawler.Crawl(ctx, crawler.Options{Bucket: bucket, Mode: m})
	if err != nil {
		return err
	}
	a.Log.Info().Str("bucket", bucket).Int64("objects", run.ObjectsScanned).
		Int64("datasets", run.DatasetsFound).Dur("took", time.Since(start)).Msg("crawl finished")
	return nil
}

func parseCrawlMode(mode string) model.CrawlMode {
	if mode == "incremental" {
		return model.CrawlIncremental
	}
	return model.CrawlFull
}
