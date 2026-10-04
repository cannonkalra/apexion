//go:build integration

// End-to-end read-options tests against a real MinIO + a real DuckDB engine.
// Excluded from the default build; run with:
//
//	APEXION_S3_ENDPOINT=localhost:19000 go test -tags integration ./internal/duckdb/...
//
// This proves the generated read_csv_auto(...) SQL actually executes against a
// gzipped, header-less CSV over S3 — the exact scenario the feature targets —
// which the string-only unit tests cannot.
package duckdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
)

func itEndpoint() string {
	if e := os.Getenv("APEXION_S3_ENDPOINT"); e != "" {
		return e
	}
	return "localhost:19000"
}

func itAccessKey() string {
	if v := os.Getenv("APEXION_S3_ACCESS_KEY"); v != "" {
		return v
	}
	return "minioadmin"
}

func itSecretKey() string {
	if v := os.Getenv("APEXION_S3_SECRET_KEY"); v != "" {
		return v
	}
	return "minioadmin"
}

func seedGzCSV(t *testing.T, bucket, key string, body string) {
	t.Helper()
	raw := s3.New(s3.Options{
		Region:                     "us-east-1",
		BaseEndpoint:               aws.String("http://" + itEndpoint()),
		UsePathStyle:               true,
		Credentials:                credentials.NewStaticCredentialsProvider(itAccessKey(), itSecretKey(), ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	})
	ctx := context.Background()
	_, _ = raw.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(body)); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if _, err := raw.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		Body: bytes.NewReader(buf.Bytes()), ContentType: aws.String("application/gzip"),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
}

// TestIntegrationReadOptionsHeaderlessGzCSV replicates the user's confirmed
// query: read_csv_auto over *.csv.gz with header=false, union_by_name, filename.
func TestIntegrationReadOptionsHeaderlessGzCSV(t *testing.T) {
	bucket := "apexion-it-ro"
	// Header-less CSV: two data rows, no header line.
	seedGzCSV(t, bucket, "feed/2023-10-11/part-0.csv.gz", "a,1\nb,2\n")

	eng, err := New(config.MinIOConfig{
		Endpoint: itEndpoint(), AccessKey: itAccessKey(), SecretKey: itSecretKey(), UseSSL: false,
	}, zerolog.Nop())
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	defer eng.Close()
	if !eng.Ready() {
		t.Skipf("duckdb httpfs unavailable (offline?): %s", eng.readErr)
	}

	ctx := context.Background()
	opts := model.ReadOptions{Filename: true, UnionByName: true, Header: "none", SampleSize: 0}
	if err := eng.CreateView(ctx, "ro_view", bucket, "feed/", model.FormatCSV, "feed/2023-10-11/part-0.csv.gz", opts); err != nil {
		t.Fatalf("CreateView: %v", err)
	}
	res, err := eng.Query(ctx, `SELECT * FROM "ro_view"`, 100)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if res.RowCount != 2 {
		t.Errorf("want 2 rows, got %d (cols=%v)", res.RowCount, res.Columns)
	}
	// header=false => DuckDB auto-names columns column0, column1 (no data row lost).
	hasCol0 := false
	hasFilename := false
	for _, c := range res.Columns {
		if c == "column0" {
			hasCol0 = true
		}
		if c == "filename" {
			hasFilename = true
		}
	}
	if !hasCol0 {
		t.Errorf("header=none should yield unnamed columns (column0…); got %v", res.Columns)
	}
	if !hasFilename {
		t.Errorf("Filename=true should expose a filename column; got %v", res.Columns)
	}
	t.Logf("columns=%v rows=%d", res.Columns, res.RowCount)
}

var _ = objstore.PreviewConfig{}
