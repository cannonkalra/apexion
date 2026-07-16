// Package crawler discovers datasets in object storage: it walks every object,
// groups them into datasets (honoring Hive partitions and table formats),
// infers schemas, catalogs metadata, and emits structured events. It supports
// worker pools, rate limiting, checkpointing, resume, and incremental crawls.
//
// Depends on: objstore, format (via the features registry), storage, events, model, config.
// Deliberately does NOT: import ui/api/http; import a concrete file-format
// reader (it asks the features registry); or run DuckDB queries / create tables
// (that is package duckdb).
package crawler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
	"github.com/apexion/apexion/internal/storage"
)

// Crawler orchestrates discovery for a bucket.
type Crawler struct {
	store     *storage.Store
	provider  objstore.Provider
	registry  *format.Registry
	resolvers map[model.Format]format.TableResolver
	bus       *events.Bus
	cfg       config.CrawlerConfig
	log       zerolog.Logger
}

// New constructs a Crawler. The provider supplies the active object store, so
// the crawler follows connection switches automatically.
func New(store *storage.Store, provider objstore.Provider, reg *format.Registry,
	resolvers map[model.Format]format.TableResolver, bus *events.Bus,
	cfg config.CrawlerConfig, log zerolog.Logger) *Crawler {
	return &Crawler{
		store: store, provider: provider, registry: reg, resolvers: resolvers,
		bus: bus, cfg: cfg, log: log.With().Str("component", "crawler").Logger(),
	}
}

// storeFor returns the store with addressing suited to the bucket name.
func (c *Crawler) storeFor(bucket string) objstore.ObjectStore { return c.provider.StoreFor(bucket) }

// Options controls a single crawl.
type Options struct {
	Bucket   string
	Prefix   string // restrict the crawl to a directory (empty = whole bucket)
	Mode     model.CrawlMode
	Trigger  model.ScheduleKind
	Resume   string // crawler_run id to resume, empty for a fresh run
	Progress func(fraction float64, message string)
}

// Crawl runs a full crawl of a bucket and returns the completed run record.
func (c *Crawler) Crawl(ctx context.Context, opts Options) (*model.CrawlerRun, error) {
	if opts.Progress == nil {
		opts.Progress = func(float64, string) {}
	}
	if opts.Mode == "" {
		opts.Mode = model.CrawlFull
	}
	if opts.Trigger == "" {
		opts.Trigger = model.ScheduleManual
	}

	bucket, err := c.ensureBucket(ctx, opts.Bucket)
	if err != nil {
		return nil, err
	}

	run, startAfter, err := c.startRun(ctx, bucket, opts)
	if err != nil {
		return nil, err
	}
	c.emit(events.TypeCrawlStarted, run.ID, events.CrawlStartedData{
		RunID: run.ID, BucketID: bucket.ID, Bucket: bucket.Name, Mode: string(opts.Mode),
	})

	opts.Progress(0.02, "listing objects")
	agg, err := c.walk(ctx, bucket, run, startAfter, opts)
	if err != nil {
		return c.failRun(ctx, run, err)
	}

	opts.Progress(0.5, fmt.Sprintf("processing %d datasets", len(agg.finalDatasets())))
	if err := c.processDatasets(ctx, bucket, run, agg, opts); err != nil {
		return c.failRun(ctx, run, err)
	}

	if err := c.store.RecomputeBucketStats(ctx, bucket.ID); err != nil {
		c.log.Warn().Err(err).Msg("recompute bucket stats")
	}
	now := time.Now().UTC()
	bucket.LastCrawlAt = &now
	bucket.UpdatedAt = now
	_ = c.store.UpsertBucket(ctx, bucket)

	run.Status = model.StatusCompleted
	run.FinishedAt = &now
	run.Checkpoint = ""
	if err := c.store.SaveCrawlerRun(ctx, run); err != nil {
		return nil, err
	}
	c.emit(events.TypeCrawlCompleted, run.ID, events.CrawlCompletedData{
		RunID: run.ID, BucketID: bucket.ID, Bucket: bucket.Name,
		ObjectsScanned: run.ObjectsScanned, DatasetsFound: run.DatasetsFound, Status: string(run.Status),
	})
	opts.Progress(1, "completed")
	c.log.Info().Str("bucket", bucket.Name).Int64("objects", run.ObjectsScanned).
		Int64("datasets", run.DatasetsFound).Msg("crawl completed")
	return run, nil
}

func (c *Crawler) ensureBucket(ctx context.Context, name string) (*model.Bucket, error) {
	// Resolve the bucket's real region up front. This primes the SDK region
	// cache so the subsequent walk signs correctly for buckets outside the
	// client's default region (essential for AWS multi-region accounts).
	cf := c.storeFor(name)
	region := cf.Region()
	if r, err := cf.BucketRegion(ctx, name); err == nil && r != "" {
		region = r
	}

	b, err := c.store.GetBucketByName(ctx, name)
	if err == nil && b != nil {
		if region != "" && b.Region != region {
			b.Region = region
			b.UpdatedAt = time.Now().UTC()
			_ = c.store.UpsertBucket(ctx, b)
		}
		return b, nil
	}
	now := time.Now().UTC()
	b = &model.Bucket{
		ID: uuid.NewString(), Name: name, Endpoint: cf.Endpoint(),
		Region: region, CreatedAt: now, UpdatedAt: now,
	}
	if err := c.store.UpsertBucket(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Crawler) startRun(ctx context.Context, bucket *model.Bucket, opts Options) (*model.CrawlerRun, string, error) {
	if opts.Resume != "" {
		run, err := c.store.GetCrawlerRun(ctx, opts.Resume)
		if err == nil && run != nil {
			run.Status = model.StatusRunning
			_ = c.store.SaveCrawlerRun(ctx, run)
			c.log.Info().Str("run", run.ID).Str("checkpoint", run.Checkpoint).Msg("resuming crawl")
			return run, run.Checkpoint, nil
		}
	}
	now := time.Now().UTC()
	run := &model.CrawlerRun{
		ID: uuid.NewString(), BucketID: bucket.ID, BucketName: bucket.Name,
		Mode: opts.Mode, Trigger: opts.Trigger, Status: model.StatusRunning, StartedAt: now,
	}
	return run, "", c.store.SaveCrawlerRun(ctx, run)
}

func (c *Crawler) failRun(ctx context.Context, run *model.CrawlerRun, cause error) (*model.CrawlerRun, error) {
	now := time.Now().UTC()
	run.Status = model.StatusFailed
	run.Error = cause.Error()
	run.FinishedAt = &now
	_ = c.store.SaveCrawlerRun(ctx, run)
	c.emit(events.TypeCrawlCompleted, run.ID, events.CrawlCompletedData{
		RunID: run.ID, BucketID: run.BucketID, Bucket: run.BucketName, Status: string(run.Status),
	})
	c.log.Error().Err(cause).Str("bucket", run.BucketName).Msg("crawl failed")
	return run, cause
}

// walk streams objects, upserts them, and builds the dataset aggregation.
func (c *Crawler) walk(ctx context.Context, bucket *model.Bucket, run *model.CrawlerRun, startAfter string, opts Options) (*aggregate, error) {
	agg := newAggregate()
	limiter := c.newLimiter()
	now := time.Now().UTC()
	incremental := opts.Mode == model.CrawlIncremental

	err := c.storeFor(bucket.Name).WalkObjects(ctx, bucket.Name, opts.Prefix, startAfter, func(om objstore.ObjectMeta) error {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		run.ObjectsScanned++

		// Table-format markers define a dataset root but are not data files.
		if root, tf, ok := tableMarker(om.Key); ok {
			agg.markTable(root, tf)
			return c.checkpoint(ctx, run, om.Key)
		}
		if shouldIgnore(om.Key, c.cfg.IgnoreHidden) {
			return c.checkpoint(ctx, run, om.Key)
		}

		f := format.DetectFormat(om.Key, nil)
		comp := format.DetectCompression(om.Key)
		hash := metadataHash(om)

		// Incremental change detection.
		objID, prevHash, existed, err := c.store.GetObjectHash(ctx, bucket.ID, om.Key)
		if err != nil {
			return err
		}
		changed := !existed || prevHash != hash
		if existed {
			if changed {
				run.ObjectsChanged++
			}
		} else {
			run.ObjectsNew++
			objID = uuid.NewString()
		}
		run.BytesScanned += om.Size

		obj := &model.Object{
			ID: objID, BucketID: bucket.ID, BucketName: bucket.Name, Key: om.Key,
			ETag: om.ETag, VersionID: om.VersionID, Size: om.Size, Format: f,
			Compression: comp, MetadataHash: hash, StorageClass: om.StorageClass,
			CreatedAt: firstNonZero(om.LastModified, now), Modified: om.LastModified,
			DiscoveredAt: now,
		}
		if !incremental || changed {
			if err := c.store.UpsertObject(ctx, obj); err != nil {
				return err
			}
		}

		pi := deriveDataset(om.Key)
		agg.add(pi, f, om, changed || !incremental)
		return c.checkpoint(ctx, run, om.Key)
	})
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	return agg, nil
}

func (c *Crawler) checkpoint(ctx context.Context, run *model.CrawlerRun, key string) error {
	if c.cfg.CheckpointEvery > 0 && run.ObjectsScanned%int64(c.cfg.CheckpointEvery) == 0 {
		run.Checkpoint = key
		return c.store.SaveCrawlerRun(ctx, run)
	}
	return nil
}

// processDatasets resolves schema for each dataset with a bounded worker pool.
func (c *Crawler) processDatasets(ctx context.Context, bucket *model.Bucket, run *model.CrawlerRun, agg *aggregate, opts Options) error {
	datasets := agg.finalDatasets()
	run.DatasetsFound = int64(len(datasets))

	g, gctx := errgroup.WithContext(ctx)
	workers := c.cfg.Workers
	if workers <= 0 {
		workers = 4
	}
	g.SetLimit(workers)

	var mu sync.Mutex
	done := 0
	for _, ds := range datasets {
		ds := ds
		g.Go(func() error {
			if err := c.processOne(gctx, bucket, ds); err != nil {
				c.log.Warn().Err(err).Str("dataset", ds.name(bucket.Name)).Msg("process dataset failed")
			}
			mu.Lock()
			done++
			frac := 0.5 + 0.5*float64(done)/float64(len(datasets))
			opts.Progress(frac, fmt.Sprintf("processed %d/%d datasets", done, len(datasets)))
			mu.Unlock()
			return nil
		})
	}
	return g.Wait()
}

// metadataHash hashes the fields that change when an object is rewritten.
func metadataHash(om objstore.ObjectMeta) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s", om.ETag, om.Size, om.VersionID, om.LastModified.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func firstNonZero(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func (c *Crawler) newLimiter() *rate.Limiter {
	if c.cfg.RateLimit <= 0 {
		return rate.NewLimiter(rate.Inf, 1)
	}
	return rate.NewLimiter(rate.Limit(c.cfg.RateLimit), c.cfg.RateLimit)
}

func (c *Crawler) emit(t events.Type, subject string, data any) {
	c.bus.Publish(events.New(t, subject, uuid.NewString(), time.Now().UTC(), data))
}

// fingerprint produces a stable hash of a field set for schema-change detection.
func fingerprint(fields []format.Field) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fmt.Sprintf("%s:%s:%v", f.Name, f.Type, f.Nullable)
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])[:32]
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
