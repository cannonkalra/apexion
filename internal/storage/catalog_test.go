package storage

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/model"
)

func memStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:", 1, zerolog.Nop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCatalogEntryCRUD(t *testing.T) {
	ctx := context.Background()
	s := memStore(t)

	e := &model.CatalogEntry{
		ID: "c1", Name: "events", DatasetID: "d1",
		BucketName: "logs", RootPath: "events", Format: model.FormatParquet,
		URI: "s3://logs/events", Enabled: true,
		RefreshMode: model.RefreshManual, SchemaStrategy: model.SchemaUnion,
		PartitionCols: []string{"year", "month"},
	}
	if err := s.UpsertCatalogEntry(ctx, e); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetCatalogEntry(ctx, "c1")
	if err != nil || got == nil {
		t.Fatalf("get: %v (nil=%v)", err, got == nil)
	}
	if got.Name != "events" || got.Format != model.FormatParquet {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.PartitionCols) != 2 || got.PartitionCols[0] != "year" {
		t.Errorf("partition cols not preserved: %v", got.PartitionCols)
	}

	byName, _ := s.GetCatalogEntryByName(ctx, "events")
	if byName == nil || byName.ID != "c1" {
		t.Errorf("get by name failed: %+v", byName)
	}

	list, err := s.ListCatalogEntries(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	if err := s.DeleteCatalogEntry(ctx, "c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, _ := s.GetCatalogEntry(ctx, "c1")
	if gone != nil {
		t.Error("entry not deleted")
	}
}

func TestCatalogEntryMissingIsNil(t *testing.T) {
	got, err := memStore(t).GetCatalogEntry(context.Background(), "nope")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing entry, got %+v", got)
	}
}
