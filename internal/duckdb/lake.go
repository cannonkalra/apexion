package duckdb

import (
	"context"
	"fmt"
	"time"
)

// This file attaches the DuckLake catalog that registered tables live in. With
// a lake attached, catalog views are created in lake.main and persist in the
// DuckLake metadata, so they survive restarts and are visible to any DuckDB
// client that ATTACHes the same lake. Without one (offline, disabled, or an
// attach error) views fall back to the engine's in-memory database.
//
// Only views are written: they read the original objects in place. Apexion
// never registers user files with ducklake_add_data_files, because DuckLake
// then owns them and its maintenance (expire + cleanup) deletes them.

// lakeAlias is the catalog name the lake is attached under.
const lakeAlias = "lake"

// LakeStatus describes the attached DuckLake catalog for display.
type LakeStatus struct {
	Attached bool
	Metadata string
	DataPath string
	Err      string // why the lake is not attached, if it was configured
	// Latest snapshot, populated by LakeInfo when attached.
	SnapshotID   int64
	SnapshotTime time.Time
}

// LakeObject is a table or view in the attached lake.
type LakeObject struct {
	Schema string
	Name   string
	Kind   string // "table" | "view"
}

// AttachLake attaches the DuckLake at metadata (a file path or any
// ducklake:<metadata> target) and makes it the default catalog, so unqualified
// table names in user queries resolve to lake.main. dataPath is passed only
// when set: DuckLake fixes it at creation and rejects a different value later.
func (e *Engine) AttachLake(ctx context.Context, metadata, dataPath string) error {
	e.lakeStatus = LakeStatus{Metadata: metadata, DataPath: dataPath}
	err := e.attachLake(ctx, metadata, dataPath)
	if err != nil {
		e.lakeStatus.Err = err.Error()
		e.log.Warn().Err(err).Str("metadata", metadata).Msg("DuckLake attach failed; catalog views will be in-memory only")
		return err
	}
	e.lake = true
	e.lakeStatus.Attached = true
	e.log.Info().Str("metadata", metadata).Msg("DuckLake catalog attached")
	return nil
}

func (e *Engine) attachLake(ctx context.Context, metadata, dataPath string) error {
	if !e.ready {
		return fmt.Errorf("query engine unavailable: %s", e.readErr)
	}
	attach := fmt.Sprintf("ATTACH 'ducklake:%s' AS %s", esc(metadata), lakeAlias)
	if dataPath != "" {
		attach += fmt.Sprintf(" (DATA_PATH '%s')", esc(dataPath))
	}
	for _, q := range []string{"INSTALL ducklake", "LOAD ducklake", attach, "USE " + lakeAlias} {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			return cleanErr(err)
		}
	}
	return nil
}

// LakeAttached reports whether catalog views live in DuckLake.
func (e *Engine) LakeAttached() bool { return e.lake }

// LakeInfo returns the lake's status plus its latest snapshot.
func (e *Engine) LakeInfo(ctx context.Context) LakeStatus {
	st := e.lakeStatus
	if !e.lake {
		return st
	}
	q := fmt.Sprintf("SELECT snapshot_id, snapshot_time FROM ducklake_snapshots('%s') ORDER BY snapshot_id DESC LIMIT 1", lakeAlias)
	_ = e.db.QueryRowContext(ctx, q).Scan(&st.SnapshotID, &st.SnapshotTime)
	return st
}

// LakeViews returns the names of the views in lake.main, so startup can create
// only the missing ones: every CREATE OR REPLACE is a new DuckLake snapshot.
func (e *Engine) LakeViews(ctx context.Context) (map[string]bool, error) {
	if !e.lake {
		return nil, nil
	}
	rows, err := e.db.QueryContext(ctx,
		"SELECT view_name FROM duckdb_views() WHERE database_name = ? AND schema_name = 'main'", lakeAlias)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// LakeObjects lists every table and view in the lake, including ones created by
// other clients sharing it.
func (e *Engine) LakeObjects(ctx context.Context) ([]LakeObject, error) {
	if !e.lake {
		return nil, nil
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT schema_name, table_name, 'table' FROM duckdb_tables() WHERE database_name = ?
		UNION ALL
		SELECT schema_name, view_name, 'view' FROM duckdb_views() WHERE database_name = ? AND NOT internal
		ORDER BY 1, 2`, lakeAlias, lakeAlias)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer rows.Close()
	var out []LakeObject
	for rows.Next() {
		var o LakeObject
		if err := rows.Scan(&o.Schema, &o.Name, &o.Kind); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// viewRef is the SQL reference for a catalog view: lake.main."name" when the
// lake is attached, else a plain quoted name in the in-memory database. name
// must already satisfy ValidIdentifier.
func viewRef(lake bool, name string) string {
	if lake {
		return fmt.Sprintf(`%s.main."%s"`, lakeAlias, name)
	}
	return `"` + name + `"`
}

// LakeHasObject reports whether lake.main already has a table or view called
// name — possibly one created by another client sharing the lake, which a
// registration's CREATE OR REPLACE must not silently overwrite.
func (e *Engine) LakeHasObject(ctx context.Context, name string) bool {
	if !e.lake {
		return false
	}
	var n int
	err := e.db.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM duckdb_tables() WHERE database_name = ? AND schema_name = 'main' AND table_name = ?)
		     + (SELECT count(*) FROM duckdb_views() WHERE database_name = ? AND schema_name = 'main' AND view_name = ?)`,
		lakeAlias, name, lakeAlias, name).Scan(&n)
	return err == nil && n > 0
}
