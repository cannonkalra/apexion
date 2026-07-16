package selection

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
)

// fakeDescriber returns canned schemas keyed by object key, with an optional
// per-call delay used to exercise cancellation.
type fakeDescriber struct {
	mu      sync.Mutex
	schemas map[string][]duckdb.ColumnDef
	errs    map[string]error
	delay   time.Duration
	calls   int
}

func (f *fakeDescriber) Ready() bool { return true }

func (f *fakeDescriber) DescribeFile(ctx context.Context, _ string, key string, _ model.Format, _ model.ReadOptions) ([]duckdb.ColumnDef, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	return f.schemas[key], nil
}

func (f *fakeDescriber) ParquetRowCount(_ context.Context, _ string) (int64, error) { return 100, nil }

func cols(pairs ...string) []duckdb.ColumnDef {
	var out []duckdb.ColumnDef
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, duckdb.ColumnDef{Name: pairs[i], Type: pairs[i+1]})
	}
	return out
}

func waitComplete(t *testing.T, svc *SelectionService, token string) ProgressSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := svc.Progress(token)
		if ok && snap.Complete {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("discovery did not complete in time")
	return ProgressSnapshot{}
}

func pq(key string) FileRef {
	return FileRef{Bucket: "b", Key: key, Format: model.FormatParquet, Size: 10}
}

func TestSelectionCompatible(t *testing.T) {
	fd := &fakeDescriber{schemas: map[string][]duckdb.ColumnDef{
		"a.parquet": cols("id", "INTEGER", "name", "VARCHAR"),
		"b.parquet": cols("id", "BIGINT", "name", "VARCHAR"),
	}}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{pq("a.parquet"), pq("b.parquet")})
	snap := waitComplete(t, svc, token)

	if snap.Summary == nil {
		t.Fatal("nil summary")
	}
	if snap.Summary.CompatCount != 2 || snap.Summary.ErrorCount != 0 {
		t.Errorf("counts compat=%d error=%d, want 2/0", snap.Summary.CompatCount, snap.Summary.ErrorCount)
	}
	if len(snap.Summary.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(snap.Summary.Tables))
	}
	if snap.Summary.RowsEst != 200 {
		t.Errorf("rows = %d, want 200", snap.Summary.RowsEst)
	}
	if snap.Summary.TotalSize != 20 {
		t.Errorf("size = %d, want 20", snap.Summary.TotalSize)
	}
}

func TestSelectionIncompatible(t *testing.T) {
	fd := &fakeDescriber{schemas: map[string][]duckdb.ColumnDef{
		"a.parquet": cols("id", "INTEGER", "price", "DOUBLE"),
		"b.parquet": cols("id", "INTEGER", "price", "VARCHAR"),
	}}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{pq("a.parquet"), pq("b.parquet")})
	snap := waitComplete(t, svc, token)

	if snap.Summary.CompatCount != 1 || snap.Summary.ErrorCount != 1 {
		t.Errorf("counts compat=%d error=%d, want 1/1", snap.Summary.CompatCount, snap.Summary.ErrorCount)
	}
	// The rejected file's verdict must be incompatible.
	var incompat int
	for _, f := range snap.Files {
		if f.Verdict == VerdictIncompatible {
			incompat++
		}
	}
	if incompat != 1 {
		t.Errorf("incompatible files = %d, want 1", incompat)
	}
}

func TestSelectionMixedFormat(t *testing.T) {
	fd := &fakeDescriber{schemas: map[string][]duckdb.ColumnDef{
		"a.parquet": cols("id", "INTEGER"),
		"a.csv":     cols("id", "INTEGER"),
	}}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{
		{Bucket: "b", Key: "a.parquet", Format: model.FormatParquet, Size: 1},
		{Bucket: "b", Key: "a.csv", Format: model.FormatCSV, Size: 1},
	})
	snap := waitComplete(t, svc, token)
	if !snap.Summary.MixedFormat || len(snap.Summary.Tables) != 2 {
		t.Errorf("mixed=%v tables=%d, want mixed with 2 tables", snap.Summary.MixedFormat, len(snap.Summary.Tables))
	}
	if snap.Summary.RowsEst != -1 {
		t.Errorf("rows = %d, want -1 (non-all-parquet)", snap.Summary.RowsEst)
	}
}

func TestSelectionErrorFile(t *testing.T) {
	fd := &fakeDescriber{
		schemas: map[string][]duckdb.ColumnDef{"a.parquet": cols("id", "INTEGER")},
		errs:    map[string]error{"bad.parquet": context.DeadlineExceeded},
	}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{pq("a.parquet"), pq("bad.parquet")})
	snap := waitComplete(t, svc, token)
	if snap.Errored != 1 || snap.Summary.ErrorCount != 1 {
		t.Errorf("errored=%d errorCount=%d, want 1/1", snap.Errored, snap.Summary.ErrorCount)
	}
}

func TestSelectionSetWithHeaderFalse(t *testing.T) {
	fd := &fakeDescriber{schemas: map[string][]duckdb.ColumnDef{
		"a.csv": cols("c0", "VARCHAR", "c1", "BIGINT"),
		"b.csv": cols("c0", "VARCHAR", "c1", "BIGINT"),
	}}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	opts := model.DefaultReadOptions()
	opts.Header = "none" // "No header row" from the Explorer options strip
	csv := func(k string) FileRef { return FileRef{Bucket: "b", Key: k, Format: model.FormatCSV, Size: 1} }
	token, _ := svc.SetWith("", []FileRef{csv("a.csv"), csv("b.csv")}, opts)
	snap := waitComplete(t, svc, token)

	pt := snap.Summary.PrimaryTable()
	if pt == nil {
		t.Fatal("no primary table")
	}
	if !strings.Contains(pt.SQL, "header=false") {
		t.Fatalf("generated SQL missing header=false:\n%s", pt.SQL)
	}
}

func TestSelectionRegenerate(t *testing.T) {
	fd := &fakeDescriber{schemas: map[string][]duckdb.ColumnDef{"a.parquet": cols("id", "INTEGER")}}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{pq("a.parquet")})
	waitComplete(t, svc, token)

	opts := model.DefaultReadOptions()
	opts.IgnoreErrors = true
	sum, ok := svc.Regenerate(token, opts)
	if !ok || sum == nil || len(sum.Tables) != 1 {
		t.Fatalf("regenerate failed: ok=%v sum=%v", ok, sum)
	}
}

func TestSelectionCancelAndReplace(t *testing.T) {
	fd := &fakeDescriber{
		schemas: map[string][]duckdb.ColumnDef{"a.parquet": cols("id", "INTEGER"), "b.parquet": cols("id", "INTEGER")},
		delay:   200 * time.Millisecond,
	}
	svc := New(context.Background(), fd, zerolog.Nop())
	defer svc.Close()

	token, _ := svc.Set("", []FileRef{pq("a.parquet")})
	// Replace before the first discovery finishes.
	token2, _ := svc.Set(token, []FileRef{pq("a.parquet"), pq("b.parquet")})
	if token2 != token {
		t.Fatalf("token changed on replace: %s -> %s", token, token2)
	}
	snap := waitComplete(t, svc, token)
	if len(snap.Files) != 2 {
		t.Errorf("files = %d, want 2 (replaced selection)", len(snap.Files))
	}
}

func TestSelectionEmpty(t *testing.T) {
	svc := New(context.Background(), &fakeDescriber{}, zerolog.Nop())
	defer svc.Close()
	token, snap := svc.Set("", nil)
	if !snap.Complete || token == "" {
		t.Errorf("empty selection: complete=%v token=%q", snap.Complete, token)
	}
}
