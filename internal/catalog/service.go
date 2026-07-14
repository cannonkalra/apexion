// Package catalog is the orchestration hub. It coordinates the crawler, the
// inference engine, background jobs, and the event bus, and exposes composite
// read models to the API and UI.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/crawler"
	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/crawler/s3"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/inference"
	"github.com/apexion/apexion/internal/jobs"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// Service coordinates crawling, inference, and cataloging.
type Service struct {
	store    *storage.Store
	provider s3.Provider
	crawler  *crawler.Crawler
	engine   *inference.Engine
	jobs     *jobs.Manager
	bus      *events.Bus
	cfg      config.InferenceConfig
	log      zerolog.Logger
}

// New constructs the catalog service.
func New(store *storage.Store, provider s3.Provider, cr *crawler.Crawler, engine *inference.Engine,
	jm *jobs.Manager, bus *events.Bus, cfg config.InferenceConfig, log zerolog.Logger) *Service {
	return &Service{
		store: store, provider: provider, crawler: cr, engine: engine, jobs: jm, bus: bus,
		cfg: cfg, log: log.With().Str("component", "catalog").Logger(),
	}
}

// ServerBuckets lists the buckets that exist on the active S3/MinIO connection
// (via the ListAllMyBuckets API). This is independent of what has been crawled
// into the catalog. The endpoint is returned so the UI can show what it is
// connected to.
func (s *Service) ServerBuckets(ctx context.Context) (endpoint string, names []string, err error) {
	client := s.provider.Client()
	names, err = client.ListBuckets(ctx)
	return client.Endpoint(), names, err
}

// Endpoint returns the active object-store endpoint.
func (s *Service) Endpoint() string { return s.provider.Client().Endpoint() }

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

// StartInference submits an inference job for a dataset.
func (s *Service) StartInference(datasetID string) (*model.Job, error) {
	ds, err := s.store.GetDataset(context.Background(), datasetID)
	if err != nil || ds == nil {
		return nil, fmt.Errorf("dataset not found")
	}
	label := fmt.Sprintf("Infer %s", ds.Name)
	return s.jobs.Submit(model.JobInference, datasetID, label, func(ctx context.Context, progress func(float64, string)) error {
		return s.RunInference(ctx, datasetID, progress)
	}), nil
}

// RunInference executes the inference engine over a dataset's stored sample and
// persists the results.
func (s *Service) RunInference(ctx context.Context, datasetID string, progress func(float64, string)) error {
	if progress == nil {
		progress = func(float64, string) {}
	}
	progress(0.05, "loading schema and sample")
	ds, err := s.store.GetDataset(ctx, datasetID)
	if err != nil || ds == nil {
		return fmt.Errorf("dataset not found")
	}
	schema, err := s.store.LatestSchema(ctx, datasetID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	run := &model.InferenceRun{
		ID: uuid.NewString(), DatasetID: datasetID, DatasetName: ds.Name,
		Status: model.StatusRunning, StartedAt: now, Findings: "{}",
	}
	_ = s.store.SaveInferenceRun(ctx, run)

	fields, rows := s.materializeSample(ctx, schema, datasetID)
	run.SampleRows = int64(len(rows))
	progress(0.3, "gathering reference columns")
	refs := s.gatherRefColumns(ctx, datasetID)

	progress(0.55, "analyzing")
	result := s.engine.Analyze(inference.Input{
		DatasetID:       datasetID,
		DatasetName:     ds.Name,
		Format:          ds.Format,
		PartitionKeys:   ds.PartitionKeys,
		Fields:          fields,
		Rows:            rows,
		RefColumns:      refs,
		MaxSampleValues: s.cfg.MaxSampleValues,
		DetectPII:       s.cfg.DetectPII,
	})

	progress(0.8, "persisting findings")
	if schema != nil {
		for _, c := range result.Columns {
			if c.SemanticType != "" {
				_ = s.store.UpdateColumnSemantic(ctx, schema.ID, c.Name, string(c.SemanticType))
			}
		}
	}

	blob, _ := json.Marshal(result)
	fin := time.Now().UTC()
	run.Status = model.StatusCompleted
	run.QualityScore = result.QualityScore
	run.Findings = string(blob)
	run.FinishedAt = &fin
	if err := s.store.SaveInferenceRun(ctx, run); err != nil {
		return err
	}

	s.bus.Publish(events.New(events.TypeInferenceCompleted, datasetID, uuid.NewString(), fin,
		events.InferenceCompletedData{
			RunID: run.ID, DatasetID: datasetID, DatasetName: ds.Name,
			QualityScore: result.QualityScore, PIIColumns: len(result.PIIColumns),
		}))
	progress(1, "completed")
	s.log.Info().Str("dataset", ds.Name).Float64("quality", result.QualityScore).Msg("inference completed")
	return nil
}

// materializeSample turns the stored sample JSON into engine input.
func (s *Service) materializeSample(ctx context.Context, schema *model.Schema, datasetID string) ([]format.Field, [][]string) {
	var fields []format.Field
	if schema != nil {
		for _, c := range schema.Columns {
			fields = append(fields, format.Field{Name: c.Name, Type: c.DataType, PhysicalType: c.PhysicalType, Nullable: c.Nullable})
		}
	}
	sample, err := s.store.GetSample(ctx, datasetID)
	if err != nil || sample == nil {
		return fields, nil
	}
	var objs []map[string]string
	if err := json.Unmarshal([]byte(sample.RowsJSON), &objs); err != nil {
		return fields, nil
	}
	// If we have no schema, derive fields from the sample keys.
	if len(fields) == 0 && len(objs) > 0 {
		seen := map[string]bool{}
		for _, o := range objs {
			for k := range o {
				if !seen[k] {
					seen[k] = true
					fields = append(fields, format.Field{Name: k, Type: model.TypeString, Nullable: true})
				}
			}
		}
	}
	rows := make([][]string, 0, len(objs))
	for _, o := range objs {
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = o[f.Name]
		}
		rows = append(rows, row)
	}
	return fields, rows
}

// gatherRefColumns builds foreign-key reference candidates from other datasets'
// key-like columns (bounded to keep it cheap).
func (s *Service) gatherRefColumns(ctx context.Context, excludeID string) []inference.RefColumn {
	datasets, err := s.store.ListDatasets(ctx, storage.DatasetFilter{Limit: 50})
	if err != nil {
		return nil
	}
	var refs []inference.RefColumn
	for _, d := range datasets {
		if d.ID == excludeID {
			continue
		}
		schema, _ := s.store.LatestSchema(ctx, d.ID)
		if schema == nil {
			continue
		}
		sample, _ := s.store.GetSample(ctx, d.ID)
		if sample == nil {
			continue
		}
		var objs []map[string]string
		if json.Unmarshal([]byte(sample.RowsJSON), &objs) != nil {
			continue
		}
		for _, c := range schema.Columns {
			if !isKeyLike(c) {
				continue
			}
			values := map[string]struct{}{}
			for _, o := range objs {
				if v, ok := o[c.Name]; ok && v != "" {
					values[v] = struct{}{}
				}
				if len(values) > 5000 {
					break
				}
			}
			if len(values) > 0 {
				refs = append(refs, inference.RefColumn{
					DatasetID: d.ID, DatasetName: d.Name, Column: c.Name, Values: values,
				})
			}
		}
	}
	return refs
}

func isKeyLike(c model.Column) bool {
	if c.Statistics != nil && c.Statistics.Uniqueness >= 0.99 && c.Statistics.DistinctCount > 1 {
		return true
	}
	n := c.Name
	return n == "id" || len(n) > 3 && n[len(n)-3:] == "_id"
}
