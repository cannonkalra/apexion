// Package lineage derives and stores the lineage graph
// (Bucket → Dataset → Table → Columns / Partitions). It is event-driven: it
// subscribes to catalog/schema events and rebuilds the affected subgraph.
package lineage

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// Service builds and serves lineage.
type Service struct {
	store *storage.Store
	log   zerolog.Logger
}

// New creates a lineage service.
func New(store *storage.Store, log zerolog.Logger) *Service {
	return &Service{store: store, log: log.With().Str("component", "lineage").Logger()}
}

// Subscribe wires the service to the event bus.
func (s *Service) Subscribe(bus *events.Bus) {
	h := events.HandlerFunc{NameStr: "lineage-builder", Fn: s.handle}
	bus.Subscribe(events.TypeSchemaChanged, h)
	bus.Subscribe(events.TypeDatasetDiscovered, h)
	bus.Subscribe(events.TypeNewPartition, h)
}

func (s *Service) handle(ctx context.Context, e events.Event) error {
	return s.BuildForDataset(ctx, e.Subject)
}

// Graph returns the full lineage graph.
func (s *Service) Graph(ctx context.Context) ([]model.LineageNode, []model.LineageEdge, error) {
	return s.store.LineageGraph(ctx)
}

// BuildForDataset rebuilds the lineage subgraph rooted at a dataset.
func (s *Service) BuildForDataset(ctx context.Context, datasetID string) error {
	ds, err := s.store.GetDataset(ctx, datasetID)
	if err != nil || ds == nil {
		return err
	}
	now := time.Now().UTC()

	bucketNode := node("bucket", ds.BucketID, ds.BucketName)
	datasetNode := node("dataset", ds.ID, ds.Name)
	if err := s.upsertNode(ctx, bucketNode, now); err != nil {
		return err
	}
	if err := s.upsertNode(ctx, datasetNode, now); err != nil {
		return err
	}
	if err := s.upsertEdge(ctx, bucketNode.ID, datasetNode.ID, "contains", now); err != nil {
		return err
	}

	// Table (schema) + columns.
	schema, err := s.store.LatestSchema(ctx, ds.ID)
	if err != nil {
		return err
	}
	if schema != nil {
		tableNode := node("table", schema.ID, ds.Name+" v"+itoa(schema.Version))
		if err := s.upsertNode(ctx, tableNode, now); err != nil {
			return err
		}
		if err := s.upsertEdge(ctx, datasetNode.ID, tableNode.ID, "derives", now); err != nil {
			return err
		}
		for _, col := range schema.Columns {
			colNode := node("column", col.ID, col.Name)
			if err := s.upsertNode(ctx, colNode, now); err != nil {
				return err
			}
			if err := s.upsertEdge(ctx, tableNode.ID, colNode.ID, "contains", now); err != nil {
				return err
			}
		}
	}

	// Partitions.
	parts, err := s.store.ListPartitions(ctx, ds.ID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		pNode := node("partition", p.ID, p.Path)
		if err := s.upsertNode(ctx, pNode, now); err != nil {
			return err
		}
		if err := s.upsertEdge(ctx, datasetNode.ID, pNode.ID, "partitions", now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertNode(ctx context.Context, n model.LineageNode, now time.Time) error {
	n.CreatedAt = now
	return s.store.UpsertLineageNode(ctx, &n)
}

func (s *Service) upsertEdge(ctx context.Context, from, to, rel string, now time.Time) error {
	e := model.LineageEdge{ID: from + "=>" + to, FromID: from, ToID: to, Relation: rel, CreatedAt: now}
	return s.store.UpsertLineageEdge(ctx, &e)
}

func node(kind, refID, label string) model.LineageNode {
	return model.LineageNode{ID: kind + ":" + refID, Kind: kind, RefID: refID, Label: label}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
