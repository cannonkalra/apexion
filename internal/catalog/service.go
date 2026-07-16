// Package catalog owns dataset registration and is the orchestration hub for
// the crawl domain. It coordinates the crawler, background jobs, and the event
// bus, and exposes composite read models (DatasetDetail) to the API and UI.
//
// Depends on: crawler, jobs, events, storage, objstore, model.
// Deliberately does NOT: import ui/api/http; talk to a storage SDK (it goes
// through objstore.Provider); or run DuckDB queries (that is package duckdb).
package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/crawler"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/jobs"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
	"github.com/apexion/apexion/internal/storage"
)

// Service coordinates crawling and cataloging.
type Service struct {
	store    *storage.Store
	provider objstore.Provider
	crawler  *crawler.Crawler
	jobs     *jobs.Manager
	bus      *events.Bus
	log      zerolog.Logger
}

// New constructs the catalog service.
func New(store *storage.Store, provider objstore.Provider, cr *crawler.Crawler,
	jm *jobs.Manager, bus *events.Bus, log zerolog.Logger) *Service {
	return &Service{
		store: store, provider: provider, crawler: cr,
		jobs: jm, bus: bus, log: log.With().Str("component", "catalog").Logger(),
	}
}

// ServerBuckets lists the buckets that exist on the active S3/MinIO connection
// (via the ListAllMyBuckets API). This is independent of what has been crawled
// into the catalog. The endpoint is returned so the UI can show what it is
// connected to. It is bounded by a short timeout so a denied or unreachable
// endpoint never hangs the Explorer — the caller falls back to cataloged
// buckets and manual entry.
func (s *Service) ServerBuckets(ctx context.Context) (endpoint string, names []string, err error) {
	store := s.provider.Store()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	names, err = store.ListBuckets(ctx)
	return store.Endpoint(), names, err
}

// Endpoint returns the active object-store endpoint.
func (s *Service) Endpoint() string { return s.provider.Store().Endpoint() }

// Store exposes the underlying store for read handlers.
func (s *Service) Store() *storage.Store { return s.store }

// PersistEvents registers a subscriber that writes every event to the store so
// the UI can render an activity feed and events are auditable.
func (s *Service) PersistEvents(bus *events.Bus) {
	bus.SubscribeAll(events.HandlerFunc{
		NameStr: "event-persister",
		Fn: func(ctx context.Context, e events.Event) error {
			return s.store.SaveEvent(ctx, &model.Event{
				ID: e.ID, Type: string(e.Type), Subject: e.Subject,
				Payload: string(e.Data), CreatedAt: e.Time,
			})
		},
	})
}

// StartCrawl submits a crawl job for a whole bucket and returns the job.
func (s *Service) StartCrawl(bucket string, mode model.CrawlMode, trigger model.ScheduleKind) *model.Job {
	return s.StartCrawlPrefix(bucket, "", mode, trigger)
}

// StartCrawlPrefix submits a crawl scoped to a directory (prefix). An empty
// prefix crawls the whole bucket.
func (s *Service) StartCrawlPrefix(bucket, prefix string, mode model.CrawlMode, trigger model.ScheduleKind) *model.Job {
	label := fmt.Sprintf("Crawl %s (%s)", bucket, mode)
	if prefix != "" {
		label = fmt.Sprintf("Crawl %s/%s (%s)", bucket, strings.TrimRight(prefix, "/"), mode)
	}
	return s.jobs.Submit(model.JobCrawl, bucket, label, func(ctx context.Context, progress func(float64, string)) error {
		_, err := s.crawler.Crawl(ctx, crawler.Options{
			Bucket: bucket, Prefix: prefix, Mode: mode, Trigger: trigger, Progress: progress,
		})
		return err
	})
}

// CancelJob requests cancellation of a running job.
func (s *Service) CancelJob(id string) bool { return s.jobs.Cancel(id) }
