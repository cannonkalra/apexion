// Package app wires every subsystem together (dependency injection) and owns
// the process lifecycle: startup, serving, and graceful shutdown.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/api"
	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/connections"
	"github.com/apexion/apexion/internal/crawler"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/httpserver"
	"github.com/apexion/apexion/internal/jobs"
	"github.com/apexion/apexion/internal/model"
	// plugins blank-imports every capability compiled into this binary so it
	// self-registers with package features before the graph is built. Build
	// tags on that package decide which optional readers/resolvers are present.
	"github.com/apexion/apexion/internal/duckdb"
	_ "github.com/apexion/apexion/internal/plugins"
	"github.com/apexion/apexion/internal/storage"
	"github.com/apexion/apexion/internal/ui"
	"github.com/apexion/apexion/pkg/logger"
)

// App holds every wired dependency and the process lifecycle handles.
type App struct {
	Cfg         *config.Config
	Log         zerolog.Logger
	Store       *storage.Store
	Bus         *events.Bus
	Connections *connections.Manager
	Crawler     *crawler.Crawler
	Catalog     *catalog.Service
	Jobs        *jobs.Manager
	Scheduler   *jobs.Scheduler
	Preview     *duckdb.Engine
	Explorer    *explorer.Service
	Server      *httpserver.Server
}

// New constructs the full application graph from configuration.
func New(ctx context.Context, cfg *config.Config) (*App, error) {
	log := logger.New(cfg.Log.Level, cfg.Log.Pretty)

	store, err := storage.Open(cfg.Storage.Path, cfg.Storage.MaxOpenConns, log)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	bus := events.NewBus(log, 4096)

	// The preview engine and the connection manager come first: the manager
	// seeds a default connection (from static config), tracks the active one,
	// hands out the active S3 client, and reconfigures the preview engine on
	// switch. Everything downstream depends on the active connection.
	prev, err := duckdb.New(cfg.MinIO, log)
	if err != nil {
		return nil, err
	}
	conns := connections.New(store, prev, cfg.MinIO, log)
	if err := conns.Init(ctx); err != nil {
		return nil, err
	}

	registry := crawler.DefaultRegistry()
	resolvers := crawler.DefaultResolvers()
	cr := crawler.New(store, conns, registry, resolvers, bus, cfg.Crawler, log)

	jobMgr := jobs.NewManager(store, log, cfg.Crawler.Workers)

	cat := catalog.New(store, conns, cr, jobMgr, bus, log)
	cat.PersistEvents(bus)

	sched := jobs.NewScheduler(store, cat, log)

	expl := explorer.New(conns)

	apiH := api.New(cat, prev, expl, conns, log)
	uiH := ui.New(cat, prev, expl, conns, cfg, log)
	srv := httpserver.New(cfg.Server, apiH, uiH, log)

	return &App{
		Cfg: cfg, Log: log, Store: store, Bus: bus, Connections: conns, Crawler: cr,
		Catalog: cat, Jobs: jobMgr, Scheduler: sched,
		Preview: prev, Explorer: expl, Server: srv,
	}, nil
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
	if a.Preview != nil {
		_ = a.Preview.Close()
	}
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
