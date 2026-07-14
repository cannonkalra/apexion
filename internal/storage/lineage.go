package storage

import (
	"context"

	"github.com/apexion/apexion/internal/model"
)

// UpsertLineageNode inserts or updates a lineage node keyed by (kind, ref_id).
func (s *Store) UpsertLineageNode(ctx context.Context, n *model.LineageNode) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO lineage_nodes (id, kind, ref_id, label, created_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT (id) DO UPDATE SET label=excluded.label`,
		n.ID, n.Kind, n.RefID, n.Label, n.CreatedAt)
	return err
}

// UpsertLineageEdge inserts a lineage edge (id is deterministic on from/to).
func (s *Store) UpsertLineageEdge(ctx context.Context, e *model.LineageEdge) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO lineage_edges (id, from_id, to_id, relation, created_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT (id) DO UPDATE SET relation=excluded.relation`,
		e.ID, e.FromID, e.ToID, e.Relation, e.CreatedAt)
	return err
}

// LineageGraph returns all nodes and edges (the full graph).
func (s *Store) LineageGraph(ctx context.Context) ([]model.LineageNode, []model.LineageEdge, error) {
	nrows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ref_id, label, created_at FROM lineage_nodes ORDER BY kind, label`)
	if err != nil {
		return nil, nil, err
	}
	defer nrows.Close()
	var nodes []model.LineageNode
	for nrows.Next() {
		var n model.LineageNode
		if err := nrows.Scan(&n.ID, &n.Kind, &n.RefID, &n.Label, &n.CreatedAt); err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, n)
	}

	erows, err := s.db.QueryContext(ctx,
		`SELECT id, from_id, to_id, relation, created_at FROM lineage_edges`)
	if err != nil {
		return nil, nil, err
	}
	defer erows.Close()
	var edges []model.LineageEdge
	for erows.Next() {
		var e model.LineageEdge
		if err := erows.Scan(&e.ID, &e.FromID, &e.ToID, &e.Relation, &e.CreatedAt); err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	return nodes, edges, nil
}
