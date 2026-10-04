package duckdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
)

func TestViewRef(t *testing.T) {
	if got := viewRef(false, "events"); got != `"events"` {
		t.Errorf("in-memory ref = %s", got)
	}
	if got := viewRef(true, "events"); got != `lake.main."events"` {
		t.Errorf("lake ref = %s", got)
	}
}

// newLocalEngine builds an engine for local-file tests. It needs the httpfs and
// ducklake extensions; the test is skipped when they cannot be installed
// (e.g. offline with an empty extension cache).
func newLocalEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(config.MinIOConfig{Region: "us-east-1"}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if !e.Ready() {
		t.Skipf("query engine unavailable: %s", e.readErr)
	}
	return e
}

func attachOrSkip(t *testing.T, e *Engine, meta string) {
	t.Helper()
	if err := e.AttachLake(context.Background(), meta, ""); err != nil {
		t.Skipf("ducklake unavailable: %v", err)
	}
}

// TestLakePersistsViews is the point of the DuckLake catalog: a view created by
// one engine is still there, and queryable unqualified, after a restart.
func TestLakePersistsViews(t *testing.T) {
	ctx := context.Background()
	meta := filepath.Join(t.TempDir(), "apexion.ducklake")

	e1 := newLocalEngine(t)
	attachOrSkip(t, e1, meta)
	if err := e1.CreateViewFromSelect(ctx, "answers", "SELECT 42 AS x"); err != nil {
		t.Fatal(err)
	}
	if err := e1.Close(); err != nil {
		t.Fatal(err)
	}

	e2 := newLocalEngine(t)
	attachOrSkip(t, e2, meta)
	views, err := e2.LakeViews(ctx)
	if err != nil || !views["answers"] {
		t.Fatalf("view not persisted: %v %v", views, err)
	}
	res, err := e2.Query(ctx, "SELECT x FROM answers", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "42" {
		t.Fatalf("rows = %v", res.Rows)
	}
	if info := e2.LakeInfo(ctx); !info.Attached || info.SnapshotID == 0 {
		t.Errorf("lake info = %+v", info)
	}

	if err := e2.DropView(ctx, "answers"); err != nil {
		t.Fatal(err)
	}
	if views, _ := e2.LakeViews(ctx); views["answers"] {
		t.Error("view still present after drop")
	}
}

// TestLakeObjectsFromOtherClients covers objects Apexion did not create: they
// are listed, and LakeHasObject reports them so registration cannot overwrite
// them.
func TestLakeObjectsFromOtherClients(t *testing.T) {
	ctx := context.Background()
	e := newLocalEngine(t)
	attachOrSkip(t, e, filepath.Join(t.TempDir(), "shared.ducklake"))

	if _, err := e.db.ExecContext(ctx, "CREATE TABLE lake.main.external_tbl (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if !e.LakeHasObject(ctx, "external_tbl") {
		t.Error("LakeHasObject missed a table created outside Apexion")
	}
	if e.LakeHasObject(ctx, "nope") {
		t.Error("LakeHasObject reported a missing name")
	}
	objs, err := e.LakeObjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0] != (LakeObject{Schema: "main", Name: "external_tbl", Kind: "table"}) {
		t.Errorf("objects = %+v", objs)
	}
}

// TestLakeAttachFailureFallsBack keeps the catalog usable when the lake cannot
// be attached: views go to the in-memory database as before.
func TestLakeAttachFailureFallsBack(t *testing.T) {
	ctx := context.Background()
	e := newLocalEngine(t)
	bad := filepath.Join(t.TempDir(), "missing-dir", "x.ducklake")
	if err := e.AttachLake(ctx, bad, ""); err == nil {
		t.Fatal("expected attach to fail for a missing directory")
	}
	if e.LakeAttached() || e.LakeInfo(ctx).Err == "" {
		t.Errorf("status = %+v", e.LakeInfo(ctx))
	}
	if err := e.CreateViewFromSelect(ctx, "mem", "SELECT 1 AS x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Query(ctx, "SELECT * FROM mem", 1); err != nil {
		t.Fatal(err)
	}
}
