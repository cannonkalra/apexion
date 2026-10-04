//go:build integration

// Integration tests that run against a real S3-compatible server. They are
// excluded from the default build; run with:
//
//	APEXION_S3_ENDPOINT=localhost:19000 go test -tags integration ./internal/objstore/s3/...
//
// (add APEXION_S3_ACCESS_KEY / APEXION_S3_SECRET_KEY for non-MinIO credentials).
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
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	pq "github.com/parquet-go/parquet-go"

	"github.com/apexion/apexion/internal/format"
	parquetfmt "github.com/apexion/apexion/internal/format/parquet"
	"github.com/apexion/apexion/internal/objstore"
)

func itEndpoint() string {
	if e := os.Getenv("APEXION_S3_ENDPOINT"); e != "" {
		return e
	}
	return "localhost:19000"
}

func itEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// newITClient connects to the test server. Credentials default to MinIO's
// (minioadmin); set APEXION_S3_ACCESS_KEY / APEXION_S3_SECRET_KEY for others,
// e.g. the local SeaweedFS (admin / password).
func newITClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(objstore.Config{
		Endpoint:  itEndpoint(),
		AccessKey: itEnv("APEXION_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: itEnv("APEXION_S3_SECRET_KEY", "minioadmin"),
		UseSSL:    false,
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

func makeBucket(c *Client, bucket string) {
	_, _ = c.s3.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
}

func put(t *testing.T, c *Client, bucket, key string, body []byte) {
	t.Helper()
	_, err := c.s3.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body),
	})
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
	c := newITClient(t)

	bucket := "apexion-it-dir"
	makeBucket(c, bucket)

	// 30 sub-folders (common prefixes), each with one file.
	wantFolders := map[string]bool{}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("dir%02d/", i)
		wantFolders[name] = true
		put(t, c, bucket, name+"leaf.txt", []byte("x"))
	}
	// One heavy folder with many objects — old resume duplicated this.
	wantFolders["heavy/"] = true
	for i := 0; i < 120; i++ {
		put(t, c, bucket, fmt.Sprintf("heavy/obj%04d.txt", i), []byte("x"))
	}
	// 400 root-level files.
	wantFiles := map[string]bool{}
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("file%04d.txt", i)
		wantFiles[key] = true
		put(t, c, bucket, key, []byte("x"))
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
	c := newITClient(t)

	const prefix = "apexion-it-pg-"
	want := map[string]bool{}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("%s%03d", prefix, i)
		want[name] = true
		makeBucket(c, name)
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

// TestIntegrationObjectReads covers the read paths the format readers use: a
// streaming GET, ranged random reads (Parquet/ORC footers) including reads that
// run past the end of the object, recursive walks, and the table-resolver
// catalog. It also checks ETags come back unquoted.
func TestIntegrationObjectReads(t *testing.T) {
	ctx := context.Background()
	c := newITClient(t)
	bucket := "apexion-it-read"
	makeBucket(c, bucket)
	body := []byte("0123456789abcdefghij") // 20 bytes
	put(t, c, bucket, "r/data.bin", body)
	put(t, c, bucket, "r/sub/meta.json", []byte(`{"v":1}`))

	src := c.NewSource(bucket, "r/data.bin", int64(len(body)))
	rc, err := src.Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	all, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(all) != string(body) {
		t.Errorf("Open read %q", all)
	}

	ra, size, err := src.ReaderAt(ctx)
	if err != nil || size != int64(len(body)) {
		t.Fatalf("ReaderAt: size=%d err=%v", size, err)
	}
	buf := make([]byte, 4)
	if n, err := ra.ReadAt(buf, 16); n != 4 || err != nil || string(buf) != "ghij" {
		t.Errorf("ReadAt(16) = %d %v %q", n, err, buf[:n])
	}
	buf = make([]byte, 8)
	if n, err := ra.ReadAt(buf, 15); n != 5 || err != io.EOF || string(buf[:n]) != "fghij" {
		t.Errorf("ReadAt past end = %d %v %q, want 5 io.EOF fghij", n, err, buf[:n])
	}
	if n, err := ra.ReadAt(buf, 20); n != 0 || err != io.EOF {
		t.Errorf("ReadAt at size = %d %v, want 0 io.EOF", n, err)
	}

	var keys []string
	err = c.WalkObjects(ctx, bucket, "r/", "", func(om objstore.ObjectMeta) error {
		keys = append(keys, om.Key)
		if strings.Contains(om.ETag, `"`) || om.ETag == "" {
			t.Errorf("ETag for %s = %q, want unquoted", om.Key, om.ETag)
		}
		return nil
	})
	if err != nil || strings.Join(keys, ",") != "r/data.bin,r/sub/meta.json" {
		t.Errorf("WalkObjects = %v %v", keys, err)
	}

	// A connection without path_style must still be addressed path-style:
	// some S3-compatible stores answer a virtual-hosted LIST with an empty,
	// successful result instead of an error.
	vh, err := New(objstore.Config{
		Endpoint:  itEndpoint(),
		AccessKey: itEnv("APEXION_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: itEnv("APEXION_S3_SECRET_KEY", "minioadmin"),
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := vh.WalkObjects(ctx, bucket, "r/", "", func(objstore.ObjectMeta) error { n++; return nil }); err != nil || n != 2 {
		t.Errorf("WalkObjects without path_style = %d objects, %v; want 2", n, err)
	}

	got, err := c.Catalog(bucket).Get(ctx, "r/sub/meta.json")
	if err != nil || string(got) != `{"v":1}` {
		t.Errorf("Catalog.Get = %q %v", got, err)
	}
	folders, files, truncated, err := c.ListDirectory(ctx, bucket, "r/", 0)
	if err != nil || truncated || len(folders) != 1 || folders[0] != "r/sub/" || len(files) != 1 || files[0].Key != "r/data.bin" {
		t.Errorf("ListDirectory = %v %v %v %v", folders, files, truncated, err)
	}
}

// countingTransport counts GET requests (ranged object reads).
type countingTransport struct {
	next http.RoundTripper
	gets atomic.Int64
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet && req.Header.Get("Range") != "" {
		t.gets.Add(1)
	}
	return t.next.RoundTrip(req)
}

// TestIntegrationParquetSchemaRead runs the real Parquet format reader (schema
// + row sampling) over a multi-MB object through Source.ReaderAt, and checks
// the block cache keeps it to a handful of ranged GETs rather than one per
// small read.
func TestIntegrationParquetSchemaRead(t *testing.T) {
	ctx := context.Background()
	c := newITClient(t)
	bucket := "apexion-it-parquet"
	makeBucket(c, bucket)

	type row struct {
		ID    int64   `parquet:"id"`
		Name  string  `parquet:"name"`
		Score float64 `parquet:"score"`
	}
	rows := make([]row, 300_000)
	for i := range rows {
		rows[i] = row{ID: int64(i), Name: fmt.Sprintf("name-%07d-%x", i, i*7919), Score: float64(i) / 3}
	}
	var buf bytes.Buffer
	if err := pq.Write(&buf, rows); err != nil {
		t.Fatal(err)
	}
	put(t, c, bucket, "big/part-0.parquet", buf.Bytes())

	ct := &countingTransport{next: http.DefaultTransport}
	opts := c.s3.Options()
	opts.HTTPClient = &http.Client{Transport: ct}
	c.s3 = s3.New(opts)

	src := c.NewSource(bucket, "big/part-0.parquet", int64(buf.Len()))
	res, err := (&parquetfmt.Reader{}).ReadSchema(ctx, src, format.Options{SampleRows: 1000})
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(res.Fields) != 3 || res.RowCountEstimate != int64(len(rows)) || len(res.Rows) != 1000 {
		t.Errorf("fields=%d rows=%d sampled=%d", len(res.Fields), res.RowCountEstimate, len(res.Rows))
	}
	gets := ct.gets.Load()
	t.Logf("%.1f MB parquet: schema + 1000 sampled rows in %d ranged GETs", float64(buf.Len())/1e6, gets)
	if gets > 8 {
		t.Errorf("ranged GETs = %d, want a handful (block cache not effective)", gets)
	}
}
