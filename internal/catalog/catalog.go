package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// ErrTableNameTaken is returned when a catalog table name is already registered.
var ErrTableNameTaken = errors.New("catalog table name already in use")

// ErrDatasetNotFound is returned when a dataset id does not resolve.
var ErrDatasetNotFound = errors.New("dataset not found")

// RegisterOptions carries the user's choices from the registration step.
type RegisterOptions struct {
	Name           string // SQL table name; empty → derived from the dataset
	Description    string
	SchemaStrategy string // union|strict|latest; empty → union
}

// RegisterDataset registers a discovered dataset as a logical SQL table with
// default options. See RegisterDatasetWith for the full form.
func (s *Service) RegisterDataset(ctx context.Context, datasetID, name string) (*model.CatalogEntry, error) {
	return s.RegisterDatasetWith(ctx, datasetID, RegisterOptions{Name: name})
}

// RegisterDatasetWith registers a discovered dataset as a logical SQL table and
// exposes it as a DuckDB view over the underlying files (no data is copied).
//
// It is transactional in effect: the catalog row is persisted first (the
// UNIQUE(name) index is the atomic gate), then the DuckDB view is created; if
// the view fails, the row is rolled back so no orphaned entry or broken view
// remains. A dataset may back several tables (different names/strategies).
func (s *Service) RegisterDatasetWith(ctx context.Context, datasetID string, opt RegisterOptions) (*model.CatalogEntry, error) {
	ds, err := s.store.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, ErrDatasetNotFound
	}
	name := opt.Name
	if name == "" {
		name = SanitizeTableName(ds.Name)
	}
	if !duckdb.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid table name %q (use letters, digits, underscore; may not start with a digit)", name)
	}
	if existing, _ := s.store.GetCatalogEntryByName(ctx, name); existing != nil {
		return nil, ErrTableNameTaken
	}
	strategy := opt.SchemaStrategy
	if strategy == "" {
		strategy = model.SchemaUnion
	}

	now := time.Now().UTC()
	entry := &model.CatalogEntry{
		ID:             uuid.NewString(),
		Name:           name,
		DatasetID:      ds.ID,
		BucketName:     ds.BucketName,
		RootPath:       ds.Path,
		Format:         ds.Format,
		URI:            fmt.Sprintf("s3://%s/%s", ds.BucketName, strings.TrimRight(ds.Path, "/")),
		Enabled:        true,
		RefreshMode:    model.RefreshManual,
		SchemaStrategy: strategy,
		PartitionCols:  ds.PartitionKeys,
		Description:    opt.Description,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// Persist first: the UNIQUE(name) index is the atomic gate. If two requests
	// race the same name, exactly one insert succeeds — the loser never creates
	// a view, so it cannot drop the winner's view. Only after the row is durably
	// ours do we create the DuckDB view; if that fails, roll the row back.
	if err := s.store.UpsertCatalogEntry(ctx, entry); err != nil {
		if existing, _ := s.store.GetCatalogEntryByName(ctx, name); existing != nil && existing.ID != entry.ID {
			return nil, ErrTableNameTaken
		}
		return nil, err
	}
	if err := s.duckdb.CreateView(ctx, name, ds.BucketName, ds.Path, ds.Format); err != nil {
		_ = s.store.DeleteCatalogEntry(ctx, entry.ID)
		return nil, fmt.Errorf("create view: %w", err)
	}
	s.log.Info().Str("table", name).Str("dataset", ds.Name).Msg("dataset registered into catalog")
	return entry, nil
}

// ListCatalog returns every catalog entry.
func (s *Service) ListCatalog(ctx context.Context) ([]model.CatalogEntry, error) {
	return s.store.ListCatalogEntries(ctx)
}

// CatalogStatus summarizes a dataset's presence in the catalog.
type CatalogStatus struct {
	Registered bool                 `json:"registered"`
	Entries    []model.CatalogEntry `json:"entries"`
}

// DatasetCatalogStatus reports whether a dataset backs any catalog tables.
func (s *Service) DatasetCatalogStatus(ctx context.Context, datasetID string) (CatalogStatus, error) {
	entries, err := s.store.ListCatalogEntriesByDataset(ctx, datasetID)
	return CatalogStatus{Registered: len(entries) > 0, Entries: entries}, err
}

// RegistrationCounts maps dataset id → number of catalog tables backed by it.
func (s *Service) RegistrationCounts(ctx context.Context) (map[string]int, error) {
	return s.store.DatasetRegistrationCounts(ctx)
}

// FindDatasetByLocation resolves the dataset a prefix-crawl produced: an exact
// path match first, else the first dataset discovered under the prefix. Returns
// nil if the crawl produced no dataset there.
func (s *Service) FindDatasetByLocation(ctx context.Context, bucketName, prefix string) (*model.Dataset, error) {
	b, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil || b == nil {
		return nil, err
	}
	trimmed := strings.Trim(prefix, "/")
	if ds, _ := s.store.GetDatasetByPath(ctx, b.ID, trimmed); ds != nil {
		return ds, nil
	}
	list, err := s.store.ListDatasets(ctx, storage.DatasetFilter{BucketID: b.ID, Search: trimmed, Limit: 1})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// SuggestTableName returns a valid, unused SQL table name derived from base
// (e.g. "events-2026" → "events_2026"), appending _2, _3, … if the name is
// already registered.
func (s *Service) SuggestTableName(ctx context.Context, base string) string {
	name := SanitizeTableName(base)
	candidate := name
	// Bounded: after a sane number of collisions, return the last candidate and
	// let registration surface ErrTableNameTaken rather than loop forever.
	for i := 2; i < 1000; i++ {
		if existing, _ := s.store.GetCatalogEntryByName(ctx, candidate); existing == nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", name, i)
	}
	return candidate
}

// GetCatalog returns a catalog entry by id (nil if absent).
func (s *Service) GetCatalog(ctx context.Context, id string) (*model.CatalogEntry, error) {
	return s.store.GetCatalogEntry(ctx, id)
}

// DeleteCatalog removes a catalog entry and drops its view.
func (s *Service) DeleteCatalog(ctx context.Context, id string) error {
	entry, err := s.store.GetCatalogEntry(ctx, id)
	if err != nil {
		return err
	}
	if entry == nil {
		return nil
	}
	if err := s.duckdb.DropView(ctx, entry.Name); err != nil {
		s.log.Warn().Err(err).Str("table", entry.Name).Msg("drop view failed")
	}
	return s.store.DeleteCatalogEntry(ctx, id)
}

// RefreshCatalog rebuilds the view (picking up format/partition changes) and
// starts a background crawl of the dataset so its metadata reflects new/removed
// files. Because the view globs the directory live, added and removed files are
// already reflected on the next query — this refreshes the stored metadata.
func (s *Service) RefreshCatalog(ctx context.Context, id string) error {
	entry, err := s.store.GetCatalogEntry(ctx, id)
	if err != nil || entry == nil {
		return err
	}
	if err := s.duckdb.CreateView(ctx, entry.Name, entry.BucketName, entry.RootPath, entry.Format); err != nil {
		return err
	}
	now := time.Now().UTC()
	entry.LastRefreshAt = &now
	entry.UpdatedAt = now
	if err := s.store.UpsertCatalogEntry(ctx, entry); err != nil {
		return err
	}
	// Re-crawl the dataset's directory so schema/statistics stay current.
	s.StartCrawlPrefix(entry.BucketName, entry.RootPath, model.CrawlIncremental, model.ScheduleManual)
	return nil
}

// RecreateViews re-registers every enabled catalog view. Called at startup
// because the DuckDB engine is in-memory and starts empty each run.
func (s *Service) RecreateViews(ctx context.Context) {
	entries, err := s.store.ListCatalogEntries(ctx)
	if err != nil {
		s.log.Warn().Err(err).Msg("list catalog entries for view recreation")
		return
	}
	n := 0
	for _, e := range entries {
		if !e.Enabled {
			continue
		}
		if err := s.duckdb.CreateView(ctx, e.Name, e.BucketName, e.RootPath, e.Format); err != nil {
			s.log.Warn().Err(err).Str("table", e.Name).Msg("recreate view failed")
			continue
		}
		n++
	}
	if n > 0 {
		s.log.Info().Int("views", n).Msg("catalog views recreated")
	}
}

// Query runs a read-only SQL statement against the catalog views.
func (s *Service) Query(ctx context.Context, sql string, limit int) (*duckdb.Result, error) {
	return s.duckdb.Query(ctx, sql, limit)
}

// SanitizeTableName derives a SQL-safe identifier from an arbitrary dataset
// name: lowercased, non-alphanumeric runs collapsed to underscores, leading
// digits prefixed with "t_".
func SanitizeTableName(name string) string {
	var b strings.Builder
	prevUS := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUS = false
		default:
			if !prevUS {
				b.WriteByte('_')
				prevUS = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "table"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "t_" + out
	}
	return out
}
