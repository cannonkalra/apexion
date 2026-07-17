//go:build integration

// Integration tests that run against a real MinIO/S3 server. They are excluded
// from the default build; run with:
//
//	APEXION_S3_ENDPOINT=localhost:19000 go test -tags integration ./internal/objstore/s3/...
//
// These cover what the fake-backed unit tests cannot: that ListPage/
// ListBucketsPage actually page correctly against S3's ListObjectsV2
// continuation-token semantics (the fake cannot catch a delimiter/cursor
// resume bug).
package s3

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/apexion/apexion/internal/objstore"
)

func itEndpoint() string {
	if e := os.Getenv("APEXION_S3_ENDPOINT"); e != "" {
		return e
	}
	return "localhost:19000"
}

func newITClient(t *testing.T) (*Client, *minio.Client) {
	t.Helper()
	cfg := objstore.Config{
		Endpoint:  itEndpoint(),
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		UseSSL:    false,
		PathStyle: true,
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	raw, err := minio.New(itEndpoint(), &minio.Options{
		Creds:        credentials.NewStaticV4("minioadmin", "minioadmin", ""),
		Secure:       false,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatalf("raw connect: %v", err)
	}
	return c, raw
}

func put(t *testing.T, raw *minio.Client, bucket, key string) {
	t.Helper()
	body := []byte("x")
	_, err := raw.PutObject(context.Background(), bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
}

// TestIntegrationListPagePaging seeds a directory whose immediate children mix
// many files and several sub-folders — including one heavy sub-folder holding
// far more objects than the page size. This is exactly the shape that broke the
// old StartAfter-based resume (a folder cursor re-collapsed into the same common
// prefix, duplicating it). It then pages through with a small limit and asserts
// the union of all pages reconstructs the true child set once, with no
// duplicates and no gaps.
func TestIntegrationListPagePaging(t *testing.T) {
	ctx := context.Background()
	c, raw := newITClient(t)

	bucket := "apexion-it-dir"
	_ = raw.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})

	// 30 sub-folders (common prefixes), each with one file.
	wantFolders := map[string]bool{}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("dir%02d/", i)
		wantFolders[name] = true
		put(t, raw, bucket, name+"leaf.txt")
	}
	// One heavy folder with many objects — old resume duplicated this.
	wantFolders["heavy/"] = true
	for i := 0; i < 120; i++ {
		put(t, raw, bucket, fmt.Sprintf("heavy/obj%04d.txt", i))
	}
	// 400 root-level files.
	wantFiles := map[string]bool{}
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("file%04d.txt", i)
		wantFiles[key] = true
		put(t, raw, bucket, key)
	}

	gotFolders := map[string]int{}
	gotFiles := map[string]int{}
	cursor := ""
	pages := 0
	for {
		pr, err := c.ListPage(ctx, bucket, "", cursor, 50)
		if err != nil {
			t.Fatalf("ListPage: %v", err)
		}
		pages++
		for _, f := range pr.Folders {
			gotFolders[f]++
		}
		for _, f := range pr.Files {
			gotFiles[f.Key]++
		}
		if !pr.HasMore {
			if pr.NextCursor != "" {
				t.Errorf("HasMore=false but NextCursor=%q", pr.NextCursor)
			}
			break
		}
		if pr.NextCursor == "" {
			t.Fatalf("HasMore=true but empty NextCursor (page %d)", pages)
		}
		cursor = pr.NextCursor
		if pages > 100 {
			t.Fatalf("did not terminate — likely a cursor-resume bug")
		}
	}

	// No duplicates.
	for f, n := range gotFolders {
		if n != 1 {
			t.Errorf("folder %q returned %d times (want 1)", f, n)
		}
	}
	for f, n := range gotFiles {
		if n != 1 {
			t.Errorf("file %q returned %d times (want 1)", f, n)
		}
	}
	// Exact set match.
	if len(gotFolders) != len(wantFolders) {
		t.Errorf("folders: got %d unique, want %d", len(gotFolders), len(wantFolders))
	}
	for f := range wantFolders {
		if gotFolders[f] == 0 {
			t.Errorf("missing folder %q", f)
		}
	}
	if len(gotFiles) != len(wantFiles) {
		t.Errorf("files: got %d unique, want %d", len(gotFiles), len(wantFiles))
	}
	for f := range wantFiles {
		if gotFiles[f] == 0 {
			t.Errorf("missing file %q", f)
		}
	}
	t.Logf("paged %d folders + %d files across %d pages", len(gotFolders), len(gotFiles), pages)
}

// TestIntegrationListBucketsPagePaging creates several buckets with a known
// prefix and pages through ListBucketsPage, asserting every one is returned
// exactly once in sorted order across pages.
func TestIntegrationListBucketsPagePaging(t *testing.T) {
	ctx := context.Background()
	c, raw := newITClient(t)

	const prefix = "apexion-it-pg-"
	want := map[string]bool{}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("%s%03d", prefix, i)
		want[name] = true
		_ = raw.MakeBucket(ctx, name, minio.MakeBucketOptions{})
	}

	got := map[string]int{}
	var order []string
	cursor := ""
	pages := 0
	for {
		names, next, hasMore, err := c.ListBucketsPage(ctx, cursor, 10)
		if err != nil {
			t.Fatalf("ListBucketsPage: %v", err)
		}
		pages++
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				got[n]++
				order = append(order, n)
			}
		}
		if !hasMore {
			break
		}
		cursor = next
		if pages > 100 {
			t.Fatalf("did not terminate")
		}
	}

	for n := range want {
		if got[n] != 1 {
			t.Errorf("bucket %q returned %d times (want 1)", n, got[n])
		}
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("bucket order across pages not sorted: %v", order)
	}
	t.Logf("paged %d matching buckets across %d pages", len(got), pages)
}
