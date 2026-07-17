package explorer

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/objstore"
)

// ---- fake ObjectStore / Provider -------------------------------------------
//
// fakeStore is an in-memory objstore.ObjectStore whose ListPage serves
// deterministic, cursor-resumable pages from a sorted key set — exactly the way
// the S3 provider does over a delimiter listing. Keys ending in "/" are folders
// (common prefixes); the rest are files. It records the prefix each ListPage was
// called with so tests can assert the explorer combined prefix+search.
type fakeStore struct {
	keys  map[string]int64 // key -> size; folder keys end in "/"
	stamp time.Time

	lastPrefix string   // prefix from the most recent ListPage call
	prefixes   []string // every prefix seen (call history)
}

func newFakeStore() *fakeStore {
	return &fakeStore{keys: map[string]int64{}, stamp: time.Unix(1_700_000_000, 0).UTC()}
}

func (f *fakeStore) addFile(key string, size int64) {
	f.keys[key] = size
}

// sortedChildren returns the immediate children of prefix, non-recursive, in
// lexicographic order — mirroring an S3 delimiter listing (the prefix key
// itself is skipped). A child is "immediate" when the remainder after prefix
// has no further "/" (a file) or exactly one trailing "/" (a folder).
func (f *fakeStore) sortedChildren(prefix string) []string {
	var out []string
	for k := range f.keys {
		if k == prefix || !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			// only immediate: nothing beyond the first slash
			if i != len(rest)-1 {
				continue
			}
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (f *fakeStore) ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (objstore.PageResult, error) {
	f.lastPrefix = prefix
	f.prefixes = append(f.prefixes, prefix)
	if limit <= 0 {
		limit = DefaultPageSize
	}
	children := f.sortedChildren(prefix)

	var res objstore.PageResult
	for _, k := range children {
		if cursor != "" && k <= cursor { // resume strictly after the cursor
			continue
		}
		if strings.HasSuffix(k, "/") {
			res.Folders = append(res.Folders, k)
		} else {
			res.Files = append(res.Files, objstore.ObjectMeta{
				Key:          k,
				Size:         f.keys[k],
				LastModified: f.stamp,
			})
		}
		if len(res.Folders)+len(res.Files) >= limit {
			res.HasMore = true
			res.NextCursor = k
			break
		}
	}
	return res, nil
}

func (f *fakeStore) ListBucketsPage(ctx context.Context, cursor string, limit int) (names []string, nextCursor string, hasMore bool, err error) {
	if limit <= 0 {
		limit = DefaultBucketPageSize
	}
	var all []string
	for k := range f.keys {
		all = append(all, k)
	}
	sort.Strings(all)
	for _, name := range all {
		if name <= cursor {
			continue
		}
		if len(names) >= limit {
			hasMore = true
			break
		}
		names = append(names, name)
	}
	if len(names) > 0 {
		nextCursor = names[len(names)-1]
	}
	return names, nextCursor, hasMore, nil
}

// ---- unused ObjectStore methods (satisfy the interface) --------------------

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

var _ objstore.ObjectStore = (*fakeStore)(nil)

// fakeProvider always hands out the same fakeStore.
type fakeProvider struct{ s *fakeStore }

func (p *fakeProvider) Store() objstore.ObjectStore                 { return p.s }
func (p *fakeProvider) StoreFor(bucket string) objstore.ObjectStore { return p.s }

var _ objstore.Provider = (*fakeProvider)(nil)

func newTestService(s *fakeStore) *Service { return New(&fakeProvider{s: s}) }

// ---- helpers ---------------------------------------------------------------

// listingKeys returns every child key (folders then files, as the service
// stores them) that a page surfaced, using Path for folders and Key for files.
func listingKeys(l *DirListing) []string {
	var out []string
	for _, fo := range l.Folders {
		out = append(out, fo.Path)
	}
	for _, fi := range l.Files {
		out = append(out, fi.Key)
	}
	return out
}

// ---- tests -----------------------------------------------------------------

// TestListDirLargeDirectoryBounded: a directory with many children returns at
// most `limit` per page, sets HasMore and a NextCursor on page one.
func TestListDirLargeDirectoryBounded(t *testing.T) {
	fs := newFakeStore()
	const total = 1200
	for i := 0; i < total; i++ {
		fs.addFile(fmtKey("f", i), int64(i))
	}
	svc := newTestService(fs)

	page, err := svc.ListDir(context.Background(), "b", "", "", SortNameAsc, "", 500)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	got := len(page.Folders) + len(page.Files)
	if got != 500 {
		t.Fatalf("page size = %d, want 500", got)
	}
	if !page.HasMore {
		t.Error("HasMore = false, want true")
	}
	if page.NextCursor == "" {
		t.Error("NextCursor empty, want set")
	}
	if page.Cursor != "" {
		t.Errorf("Cursor = %q, want empty on first page", page.Cursor)
	}
}

// TestListDirDefaultPageSize: limit<=0 uses DefaultPageSize.
func TestListDirDefaultPageSize(t *testing.T) {
	fs := newFakeStore()
	for i := 0; i < DefaultPageSize+50; i++ {
		fs.addFile(fmtKey("f", i), 1)
	}
	svc := newTestService(fs)
	page, err := svc.ListDir(context.Background(), "b", "", "", SortNameAsc, "", 0)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if n := len(page.Folders) + len(page.Files); n != DefaultPageSize {
		t.Fatalf("page size = %d, want %d", n, DefaultPageSize)
	}
	if page.Limit != DefaultPageSize {
		t.Errorf("Limit = %d, want %d", page.Limit, DefaultPageSize)
	}
}

// TestListDirPaginationNoOverlapNoGap: paging with cursor=NextCursor walks the
// whole child set exactly once — no overlap, no gap — ending with HasMore=false
// and NextCursor="".
func TestListDirPaginationCursorCorrectness(t *testing.T) {
	fs := newFakeStore()
	const total = 1050
	want := make([]string, 0, total)
	for i := 0; i < total; i++ {
		k := fmtKey("obj", i)
		fs.addFile(k, int64(i))
		want = append(want, k)
	}
	sort.Strings(want)
	svc := newTestService(fs)

	var got []string
	cursor := ""
	pages := 0
	for {
		page, err := svc.ListDir(context.Background(), "b", "", "", SortNameAsc, cursor, 400)
		if err != nil {
			t.Fatalf("ListDir page %d: %v", pages, err)
		}
		if page.Cursor != cursor {
			t.Errorf("page %d Cursor = %q, want %q", pages, page.Cursor, cursor)
		}
		got = append(got, listingKeys(page)...)
		pages++
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Errorf("final page NextCursor = %q, want empty", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatalf("page %d HasMore but empty NextCursor", pages)
		}
		cursor = page.NextCursor
		if pages > 100 {
			t.Fatal("too many pages — cursor not advancing")
		}
	}

	if len(got) != len(want) {
		t.Fatalf("reconstructed %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key[%d] = %q, want %q (overlap or gap)", i, got[i], want[i])
		}
	}
	// exactly once: no duplicates
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Fatalf("duplicate key across pages: %q", k)
		}
		seen[k] = true
	}
}

// TestListDirBackNavigationAndRefresh: re-fetching cursor="" always yields the
// identical first page (stateless), proving both back-navigation to page 1 and
// refresh return fresh page 1.
func TestListDirBackNavigationAndRefresh(t *testing.T) {
	fs := newFakeStore()
	for i := 0; i < 900; i++ {
		fs.addFile(fmtKey("f", i), int64(i))
	}
	svc := newTestService(fs)
	ctx := context.Background()

	first, err := svc.ListDir(ctx, "b", "", "", SortNameAsc, "", 300)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	// page deeper
	deeper, err := svc.ListDir(ctx, "b", "", "", SortNameAsc, first.NextCursor, 300)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if deeper.Cursor == "" {
		t.Fatal("second page cursor unexpectedly empty")
	}
	// back to page 1 (and again = refresh) must equal the original first page
	for _, label := range []string{"back", "refresh"} {
		again, err := svc.ListDir(ctx, "b", "", "", SortNameAsc, "", 300)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if !equalKeys(listingKeys(first), listingKeys(again)) {
			t.Errorf("%s page != first page", label)
		}
		if again.NextCursor != first.NextCursor {
			t.Errorf("%s NextCursor = %q, want %q", label, again.NextCursor, first.NextCursor)
		}
	}
}

// TestListDirSearchCombinesPrefix: search narrows results and the fake store
// receives prefix+search as the combined ListPage prefix.
func TestListDirSearchCombinesPrefix(t *testing.T) {
	fs := newFakeStore()
	fs.addFile("data/alpha.csv", 1)
	fs.addFile("data/apple.csv", 2)
	fs.addFile("data/banana.csv", 3)
	svc := newTestService(fs)

	page, err := svc.ListDir(context.Background(), "b", "data/", "a", SortNameAsc, "", 100)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if fs.lastPrefix != "data/a" {
		t.Errorf("ListPage prefix = %q, want %q", fs.lastPrefix, "data/a")
	}
	if len(page.Files) != 2 {
		t.Fatalf("files = %d, want 2 (alpha, apple)", len(page.Files))
	}
	for _, f := range page.Files {
		if !strings.HasPrefix(f.Name, "a") {
			t.Errorf("file %q leaked past prefix filter", f.Name)
		}
	}
	if page.Search != "a" {
		t.Errorf("Search = %q, want %q", page.Search, "a")
	}
}

// TestListDirSortPerPage: sorting orders within the returned page (size_desc
// here). Only per-page ordering is asserted (S3 can't sort globally).
func TestListDirSortPerPage(t *testing.T) {
	fs := newFakeStore()
	fs.addFile("d/a.bin", 10)
	fs.addFile("d/b.bin", 50)
	fs.addFile("d/c.bin", 30)
	fs.addFile("d/z/", 0) // a folder (sorts by name)
	svc := newTestService(fs)

	page, err := svc.ListDir(context.Background(), "b", "d/", "", SortSizeDesc, "", 100)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(page.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(page.Files))
	}
	for i := 1; i < len(page.Files); i++ {
		if page.Files[i-1].Size < page.Files[i].Size {
			t.Errorf("size_desc violated at %d: %d < %d", i, page.Files[i-1].Size, page.Files[i].Size)
		}
	}
}

// TestListBucketsPageDelegatesAndPages: ListBucketsPage delegates to the store
// and pages via the cursor across the sorted bucket set.
func TestListBucketsPageDelegatesAndPages(t *testing.T) {
	fs := newFakeStore()
	want := []string{}
	for _, n := range []string{"echo", "alpha", "delta", "charlie", "bravo"} {
		fs.addFile(n, 1) // fake buckets are just keys
		want = append(want, n)
	}
	sort.Strings(want)
	svc := newTestService(fs)
	ctx := context.Background()

	var got []string
	cursor := ""
	for {
		names, next, more, err := svc.ListBucketsPage(ctx, cursor, 2)
		if err != nil {
			t.Fatalf("ListBucketsPage: %v", err)
		}
		got = append(got, names...)
		if !more {
			break
		}
		if next == "" {
			t.Fatal("HasMore but empty cursor")
		}
		cursor = next
	}
	if !equalKeys(want, got) {
		t.Errorf("buckets = %v, want %v", got, want)
	}
}

// ---- small helpers ---------------------------------------------------------

func fmtKey(prefix string, i int) string {
	// zero-pad so lexicographic order == numeric order. Flat key (no interior
	// slash) so it is an immediate child of the root prefix.
	return prefix + "-" + pad(i) + ".dat"
}

func pad(i int) string {
	s := ""
	for _, d := range []int{1000000, 100000, 10000, 1000, 100, 10, 1} {
		s += string(rune('0' + (i/d)%10))
	}
	return s
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
