package jobs

import (
	"context"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// CrawlStarter is implemented by the catalog service; it lets the scheduler
// trigger crawls without importing the catalog package (avoids a cycle).
type CrawlStarter interface {
	StartCrawl(bucket string, mode model.CrawlMode, trigger model.ScheduleKind) *model.Job
}

// Scheduler runs cron-based incremental crawls for buckets that define a
// schedule. Manual and full crawls go through the catalog service directly.
type Scheduler struct {
	cron    *cron.Cron
	store   *storage.Store
	starter CrawlStarter
	log     zerolog.Logger
}

// NewScheduler creates a scheduler.
func NewScheduler(store *storage.Store, starter CrawlStarter, log zerolog.Logger) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		store:   store,
		starter: starter,
		log:     log.With().Str("component", "scheduler").Logger(),
	}
}

// Start loads bucket schedules and starts the cron loop.
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	s.cron.Start()
	s.log.Info().Int("entries", len(s.cron.Entries())).Msg("scheduler started")
	return nil
}

// Reload rebuilds cron entries from the current bucket schedules.
func (s *Scheduler) Reload(ctx context.Context) error {
	for _, e := range s.cron.Entries() {
		s.cron.Remove(e.ID)
	}
	buckets, err := s.store.ListBuckets(ctx)
	if err != nil {
		return err
	}
	for _, b := range buckets {
		if b.Schedule == "" {
			continue
		}
		bucket := b.Name
		schedule := b.Schedule
		if _, err := s.cron.AddFunc(schedule, func() {
			s.log.Info().Str("bucket", bucket).Str("cron", schedule).Msg("scheduled crawl")
			s.starter.StartCrawl(bucket, model.CrawlIncremental, model.ScheduleCron)
		}); err != nil {
			s.log.Warn().Err(err).Str("bucket", bucket).Str("cron", schedule).Msg("invalid schedule")
		}
	}
	return nil
}

// Stop halts the scheduler and waits for running cron jobs.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}
