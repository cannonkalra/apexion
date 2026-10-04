package s3

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/apexion/apexion/internal/objstore"
)

func TestNeedsPathStyle(t *testing.T) {
	cases := map[string]bool{
		"eyeota-data-feed": false, // DNS-compliant → virtual-host
		"warehouse":        false,
		"my.bucket":        true, // dot breaks virtual-host + TLS
		"Dharani_test":     true, // uppercase + underscore
		"UPPER":            true,
		"has_underscore":   true,
		"":                 false,
	}
	for bucket, want := range cases {
		if got := needsPathStyle(bucket); got != want {
			t.Errorf("needsPathStyle(%q) = %v, want %v", bucket, got, want)
		}
	}
}

func TestIsAWS(t *testing.T) {
	yes := []objstore.Config{
		{Provider: "aws", Endpoint: ""},
		{Provider: "minio", Endpoint: "s3.amazonaws.com"},
		{Provider: "", Endpoint: "bucket.s3.us-east-1.amazonaws.com"},
	}
	for _, c := range yes {
		if !isAWS(c) {
			t.Errorf("isAWS(%q,%q) = false, want true", c.Provider, c.Endpoint)
		}
	}
	if isAWS(objstore.Config{Provider: "minio", Endpoint: "localhost:9000"}) {
		t.Error("isAWS(minio, localhost) = true, want false")
	}
}

func TestPageBuckets(t *testing.T) {
	// pageBuckets is the synthetic S3 bucket-paging slice logic (S3 has no native
	// bucket pagination). The full ListPage/ListBucketsPage client methods drive
	// the S3 SDK and can only be exercised against a live server, so they are
	// covered by the explorer/api service tests via a fake store rather than a
	// network test here.
	all := []string{"echo", "alpha", "delta", "charlie", "bravo"} // unsorted input

	// Page 1: sorted, bounded by limit, HasMore set.
	names, next, more := pageBuckets(all, "", 2)
	if want := []string{"alpha", "bravo"}; !equalStrs(names, want) {
		t.Fatalf("page1 = %v, want %v", names, want)
	}
	if next != "bravo" || !more {
		t.Fatalf("page1 next=%q more=%v, want bravo/true", next, more)
	}

	// Page 2: resume strictly after the cursor.
	names, next, more = pageBuckets(all, "bravo", 2)
	if want := []string{"charlie", "delta"}; !equalStrs(names, want) {
		t.Fatalf("page2 = %v, want %v", names, want)
	}
	if next != "delta" || !more {
		t.Fatalf("page2 next=%q more=%v, want delta/true", next, more)
	}

	// Final page: fewer than limit remain, HasMore=false.
	names, _, more = pageBuckets(all, "delta", 2)
	if want := []string{"echo"}; !equalStrs(names, want) {
		t.Fatalf("page3 = %v, want %v", names, want)
	}
	if more {
		t.Fatal("page3 HasMore = true, want false")
	}

	// Cursor past the end: empty page, no cursor, no more.
	names, next, more = pageBuckets(all, "zzz", 2)
	if len(names) != 0 || next != "" || more {
		t.Fatalf("beyond-end = %v/%q/%v, want empty/empty/false", names, next, more)
	}

	// Reconstruct the full sorted set across pages exactly once.
	var got []string
	cursor := ""
	for {
		page, nc, hm := pageBuckets(all, cursor, 2)
		got = append(got, page...)
		if !hm {
			break
		}
		cursor = nc
	}
	if want := []string{"alpha", "bravo", "charlie", "delta", "echo"}; !equalStrs(got, want) {
		t.Fatalf("reconstructed = %v, want %v", got, want)
	}

	// limit<=0 is the caller's responsibility (ListBucketsPage substitutes
	// defaultPageSize before calling); pageBuckets with limit 0 yields nothing.
	if names, _, more := pageBuckets(all, "", 0); len(names) != 0 || !more {
		t.Fatalf("limit 0 = %v/%v, want empty/HasMore", names, more)
	}
}

func equalStrs(a, b []string) bool {
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

func TestEndpointURL(t *testing.T) {
	cases := []struct {
		cfg  objstore.Config
		want string // "" = nil (SDK resolves AWS endpoints)
	}{
		{objstore.Config{Endpoint: "localhost:8333"}, "http://localhost:8333"},
		{objstore.Config{Endpoint: "minio.internal:9000", UseSSL: true}, "https://minio.internal:9000"},
		{objstore.Config{Endpoint: "https://r2.example.com"}, "https://r2.example.com"},
		{objstore.Config{Provider: "aws", Endpoint: ""}, ""},
		{objstore.Config{Provider: "aws", Endpoint: "s3.amazonaws.com"}, ""},
		{objstore.Config{Provider: "aws", Endpoint: "s3.eu-west-1.amazonaws.com"}, ""},
		{objstore.Config{Provider: "aws", Endpoint: "s3-us-west-2.amazonaws.com"}, ""},
		// A VPC interface endpoint is used as given.
		{objstore.Config{Provider: "aws", Endpoint: "bucket.vpce-0a1b.s3.us-east-1.vpce.amazonaws.com", UseSSL: true},
			"https://bucket.vpce-0a1b.s3.us-east-1.vpce.amazonaws.com"},
	}
	for _, c := range cases {
		got := ""
		if p := endpointURL(c.cfg); p != nil {
			got = *p
		}
		if got != c.want {
			t.Errorf("endpointURL(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

func TestObjectMetaUnquotesETag(t *testing.T) {
	key, etag, size := "a/b.parquet", `"9b2cf535f27731c974343645a3985328"`, int64(42)
	om := objectMeta(types.Object{Key: &key, ETag: &etag, Size: &size, StorageClass: types.ObjectStorageClassStandardIa})
	if om.ETag != "9b2cf535f27731c974343645a3985328" {
		t.Errorf("ETag = %q, want it unquoted", om.ETag)
	}
	if om.Key != key || om.Size != size || om.StorageClass != "STANDARD_IA" {
		t.Errorf("objectMeta = %+v", om)
	}
}

func TestUsePathStyle(t *testing.T) {
	cases := []struct {
		cfg  objstore.Config
		want bool
	}{
		{objstore.Config{Endpoint: "localhost:8333"}, true}, // S3-compatible: always path-style
		{objstore.Config{Provider: "minio", Endpoint: "minio:9000"}, true},
		{objstore.Config{Provider: "aws"}, false}, // AWS: virtual-hosted by default
		{objstore.Config{Provider: "aws", PathStyle: true}, true},
	}
	for _, c := range cases {
		if got := usePathStyle(c.cfg); got != c.want {
			t.Errorf("usePathStyle(%+v) = %v, want %v", c.cfg, got, c.want)
		}
	}
}
