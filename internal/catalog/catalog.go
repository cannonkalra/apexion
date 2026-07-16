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
)

// ErrTableNameTaken is returned when a catalog table name is already registered.
var ErrTableNameTaken = errors.New("catalog table name already in use")

// ErrDatasetNotFound is returned when a dataset id does not resolve.
var ErrDatasetNotFound = errors.New("dataset not found")

// RegisterDataset registers a discovered dataset as a logical SQL table and
// exposes it as a DuckDB view over the underlying files (no data is copied). If
// name is empty, a SQL-safe name is derived from the dataset.
func (s *Service) RegisterDataset(ctx context.Context, datasetID, name string) (*model.CatalogEntry, error) {
	ds, err := s.store.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, ErrDatasetNotFound
	}
	if name == "" {
		name = SanitizeTableName(ds.Name)
	}
	if !duckdb.ValidIdentifier(name) {
		return nil, fmt.Errorf("invalid table name %q (use letters, digits, underscore)", name)
	}
	if existing, _ := s.store.GetCatalogEntryByName(ctx, name); existing != nil {
		return nil, ErrTableNameTaken
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
		SchemaStrategy: model.SchemaUnion,
		PartitionCols:  ds.PartitionKeys,
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
