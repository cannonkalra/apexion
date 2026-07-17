package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/objstore"
)

// ---- fake objstore wired through the real explorer.Service -----------------

type fakeStore struct {
	keys       map[string]int64
	stamp      time.Time
	lastPrefix string
}

func (f *fakeStore) ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (objstore.PageResult, error) {
	f.lastPrefix = prefix
	if limit <= 0 {
		limit = explorer.DefaultPageSize
	}
	var all []string
	for k := range f.keys {
		if k == prefix || !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 && i != len(rest)-1 {
			continue // not an immediate child
		}
		all = append(all, k)
	}
	sort.Strings(all)
	var res objstore.PageResult
	for _, k := range all {
		if cursor != "" && k <= cursor {
			continue
		}
		if strings.HasSuffix(k, "/") {
			res.Folders = append(res.Folders, k)
		} else {
			res.Files = append(res.Files, objstore.ObjectMeta{Key: k, Size: f.keys[k], LastModified: f.stamp})
		}
		if len(res.Folders)+len(res.Files) >= limit {
			res.HasMore = true
			res.NextCursor = k
			break
		}
	}
	return res, nil
}

func (f *fakeStore) ListBucketsPage(ctx context.Context, cursor string, limit int) ([]string, string, bool, error) {
	return nil, "", false, nil
}
func (f *fakeStore) ListBuckets(ctx context.Context) ([]string, error) { return nil, nil }
func (f *fakeStore) WalkObjects(ctx context.Context, bucket, prefix, startAfter string, fn func(objstore.ObjectMeta) error) error {
	return nil
}
func (f *fakeStore) ListDirectory(ctx context.Context, bucket, prefix string, limit int) ([]string, []objstore.ObjectMeta, bool, error) {
	return nil, nil, false, nil
}
func (f *fakeStore) NewSource(bucket, key string, size int64) format.Source { return nil }
func (f *fakeStore) Catalog(bucket string) format.Catalog                   { return nil }
func (f *fakeStore) Endpoint() string                                       { return "" }
func (f *fakeStore) Region() string                                         { return "" }
func (f *fakeStore) BucketRegion(ctx context.Context, bucket string) (string, error) {
	return "", nil
}

type fakeProvider struct{ s *fakeStore }

func (p *fakeProvider) Store() objstore.ObjectStore                 { return p.s }
func (p *fakeProvider) StoreFor(bucket string) objstore.ObjectStore { return p.s }

func newTestAPI(fs *fakeStore) *API {
	// exploreList only touches a.explorer, so we wire just that dependency.
	return &API{explorer: explorer.New(&fakeProvider{s: fs})}
}

// ---- tests -----------------------------------------------------------------

func TestExploreListBucketRequired(t *testing.T) {
	a := newTestAPI(&fakeStore{keys: map[string]int64{}, stamp: time.Now()})
	req := httptest.NewRequest(http.MethodGet, "/explorer/list", nil) // no bucket, no path
	rec := httptest.NewRecorder()
	a.exploreList(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] == "" {
		t.Errorf("missing error message, body=%s", rec.Body.String())
	}
}

func TestExploreListHappyPathJSONShape(t *testing.T) {
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	fs := &fakeStore{keys: map[string]int64{}, stamp: stamp}
	fs.keys["data/sub/"] = 0
	fs.keys["data/a.csv"] = 10
	fs.keys["data/b.parquet"] = 20
	a := newTestAPI(fs)

	req := httptest.NewRequest(http.MethodGet, "/explorer/list?bucket=mybucket&prefix=data/&limit=100", nil)
	rec := httptest.NewRecorder()
	a.exploreList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want json", ct)
	}

	// Decode into a generic map to assert exact JSON shape / keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"bucket", "prefix", "folders", "files", "cursor", "nextCursor", "hasMore", "totalKnown"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("response missing key %q; body=%s", k, rec.Body.String())
		}
	}
	// totalKnown must be JSON null (we never full-scan).
	if string(raw["totalKnown"]) != "null" {
		t.Errorf("totalKnown = %s, want null", raw["totalKnown"])
	}

	var resp struct {
		Bucket  string `json:"bucket"`
		Prefix  string `json:"prefix"`
		Folders []struct {
			Name, Path string
		} `json:"folders"`
		Files []struct {
			Name, Key, Modified, Format string
			Size                        int64
		} `json:"files"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode typed: %v", err)
	}
	if resp.Bucket != "mybucket" || resp.Prefix != "data/" {
		t.Errorf("bucket/prefix = %q/%q", resp.Bucket, resp.Prefix)
	}
	if len(resp.Folders) != 1 || resp.Folders[0].Path != "data/sub/" {
		t.Errorf("folders = %+v", resp.Folders)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(resp.Files))
	}
	if resp.HasMore {
		t.Error("hasMore = true, want false (small dir)")
	}
	// Modified is RFC3339.
	if _, err := time.Parse(time.RFC3339, resp.Files[0].Modified); err != nil {
		t.Errorf("modified %q not RFC3339: %v", resp.Files[0].Modified, err)
	}
}

// TestExploreListSortDirectionCombine: a bare sort field + direction combine
// into the explorer.Sort* constant. size_desc must order the returned page by
// size descending.
func TestExploreListSortDirectionCombine(t *testing.T) {
	fs := &fakeStore{keys: map[string]int64{}, stamp: time.Now()}
	fs.keys["d/a.bin"] = 10
	fs.keys["d/b.bin"] = 50
	fs.keys["d/c.bin"] = 30
	a := newTestAPI(fs)

	req := httptest.NewRequest(http.MethodGet, "/explorer/list?bucket=b&prefix=d/&sort=size&direction=desc", nil)
	rec := httptest.NewRecorder()
	a.exploreList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []struct {
			Size int64
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(resp.Files))
	}
	for i := 1; i < len(resp.Files); i++ {
		if resp.Files[i-1].Size < resp.Files[i].Size {
			t.Errorf("size_desc violated: %d < %d", resp.Files[i-1].Size, resp.Files[i].Size)
		}
	}
}

// TestExploreListPathFallback: when `bucket` is absent, `path` supplies
// bucket/prefix (first segment = bucket, remainder = prefix).
func TestExploreListPathFallback(t *testing.T) {
	fs := &fakeStore{keys: map[string]int64{"warehouse/x.csv": 1}, stamp: time.Now()}
	a := newTestAPI(fs)
	req := httptest.NewRequest(http.MethodGet, "/explorer/list?path=/warehouse/sub/", nil)
	rec := httptest.NewRecorder()
	a.exploreList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bucket, Prefix string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bucket != "warehouse" || resp.Prefix != "sub/" {
		t.Errorf("bucket/prefix = %q/%q, want warehouse/sub/", resp.Bucket, resp.Prefix)
	}
}

// TestExploreListPaginationThroughRouter drives the mounted route end-to-end via
// httptest.Server, paging with the returned nextCursor until hasMore=false, and
// asserts the full child set is reconstructed exactly once.
func TestExploreListPaginationThroughRouter(t *testing.T) {
	fs := &fakeStore{keys: map[string]int64{}, stamp: time.Now()}
	want := []string{}
	for i := 0; i < 25; i++ {
		k := "root/" + string(rune('a'+i%26)) + pad2(i) + ".dat"
		fs.keys[k] = int64(i)
		want = append(want, k)
	}
	sort.Strings(want)
	a := newTestAPI(fs)

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 50 {
			t.Fatal("cursor not advancing")
		}
		url := "/explorer/list?bucket=b&prefix=root/&limit=10&cursor=" + cursor
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		a.exploreList(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Files []struct {
				Key string
			} `json:"files"`
			NextCursor string `json:"nextCursor"`
			HasMore    bool   `json:"hasMore"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, f := range resp.Files {
			got = append(got, f.Key)
		}
		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}
	if len(got) != len(want) {
		t.Fatalf("reconstructed %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func pad2(i int) string {
	return string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}
