// Command seed uploads a set of realistic sample datasets to MinIO so Apexion
// has something to discover: CSV (with PII), Parquet, JSONL, a Hive-partitioned
// Parquet table, and a Delta Lake table.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	pq "github.com/parquet-go/parquet-go"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	endpoint := env("APEXION_MINIO_ENDPOINT", "localhost:9000")
	access := env("APEXION_MINIO_ACCESS_KEY", "minioadmin")
	secret := env("APEXION_MINIO_SECRET_KEY", "minioadmin")
	bucket := env("SEED_BUCKET", "warehouse")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: false,
	})
	must(err)

	ctx := context.Background()
	exists, _ := mc.BucketExists(ctx, bucket)
	if !exists {
		must(mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}))
	}

	put := func(key string, data []byte, ct string) {
		_, err := mc.PutObject(ctx, bucket, key, bytes.NewReader(data), int64(len(data)),
			minio.PutObjectOptions{ContentType: ct})
		must(err)
		fmt.Printf("  uploaded %s/%s (%d bytes)\n", bucket, key, len(data))
	}

	fmt.Println("Seeding sample datasets…")

	// 1. CSV with PII.
	put("customers/customers.csv", []byte(customersCSV), "text/csv")

	// 2. JSONL with nested objects/arrays.
	put("events/events.jsonl", []byte(eventsJSONL), "application/x-ndjson")

	// 3. Standalone Parquet.
	put("products/products.parquet", productsParquet(), "application/octet-stream")

	// 4. Hive-partitioned Parquet (orders/year=/month=/).
	put("orders/year=2023/month=01/part-0000.parquet", ordersParquet(2023, 1), "application/octet-stream")
	put("orders/year=2023/month=02/part-0000.parquet", ordersParquet(2023, 2), "application/octet-stream")
	put("orders/year=2024/month=01/part-0000.parquet", ordersParquet(2024, 1), "application/octet-stream")

	// 5. Delta Lake table (data parquet + _delta_log commit).
	put("sales_delta/part-00000.parquet", salesParquet(), "application/octet-stream")
	put("sales_delta/_delta_log/00000000000000000000.json", []byte(deltaLog), "application/json")

	fmt.Println("Done. Now run: apexion crawl", bucket)
}

// ---- sample data ---------------------------------------------------------

const customersCSV = `id,email,first_name,last_name,phone,country,signup_date,balance
1,alice@example.com,Alice,Martin,+1-202-555-0143,US,2023-01-15,1024.50
2,bob@example.org,Bob,Nguyen,+44 20 7946 0958,GB,2023-02-03,89.00
3,carol@test.io,Carol,Silva,+55 11 91234-5678,BR,2023-02-19,4500.75
4,dan@example.com,Dan,Kim,+82 2 1234 5678,KR,2023-03-01,0.00
5,eve@example.net,Eve,Rossi,+39 06 6982 1234,IT,2023-03-22,310.20
6,frank@example.com,Frank,Weber,+49 30 123456,DE,2023-04-10,720.00
7,grace@test.io,Grace,Dubois,+33 1 4289 1234,FR,2023-05-05,1580.99
8,heidi@example.org,Heidi,Andersson,+46 8 123 456,SE,2023-05-30,42.10`

const eventsJSONL = `{"event_id":"a1b2","user_id":1,"type":"click","props":{"page":"/home","btn":"cta"},"tags":["web","us"],"ts":"2023-06-01T10:00:00Z"}
{"event_id":"c3d4","user_id":2,"type":"view","props":{"page":"/pricing"},"tags":["web"],"ts":"2023-06-01T10:05:00Z"}
{"event_id":"e5f6","user_id":3,"type":"purchase","props":{"amount":49.99,"currency":"USD"},"tags":["web","conv"],"ts":"2023-06-01T10:12:00Z"}
{"event_id":"g7h8","user_id":1,"type":"click","props":{"page":"/docs"},"tags":["web"],"ts":"2023-06-01T10:20:00Z"}`

const deltaLog = `{"commitInfo":{"timestamp":1700000000000,"operation":"WRITE"}}
{"protocol":{"minReaderVersion":1,"minWriterVersion":2}}
{"metaData":{"id":"sales-delta-0001","format":{"provider":"parquet"},"schemaString":"{\"type\":\"struct\",\"fields\":[{\"name\":\"sale_id\",\"type\":\"long\",\"nullable\":false,\"metadata\":{}},{\"name\":\"region\",\"type\":\"string\",\"nullable\":true,\"metadata\":{}},{\"name\":\"revenue\",\"type\":\"double\",\"nullable\":true,\"metadata\":{}},{\"name\":\"sold_at\",\"type\":\"timestamp\",\"nullable\":true,\"metadata\":{}}]}","partitionColumns":["region"],"configuration":{},"createdTime":1700000000000}}
{"add":{"path":"part-00000.parquet","partitionValues":{},"size":1024,"modificationTime":1700000000000,"dataChange":true}}`

// ---- parquet writers -----------------------------------------------------

type product struct {
	ID       int64   `parquet:"id"`
	Name     string  `parquet:"name"`
	Category string  `parquet:"category"`
	Price    float64 `parquet:"price"`
	InStock  bool    `parquet:"in_stock"`
}

func productsParquet() []byte {
	rows := []product{
		{1, "Widget", "hardware", 9.99, true},
		{2, "Gadget", "hardware", 19.95, true},
		{3, "Gizmo", "electronics", 4.50, false},
		{4, "Sprocket", "hardware", 2.25, true},
		{5, "Doohickey", "electronics", 14.00, true},
	}
	return writeParquet(rows)
}

type order struct {
	OrderID    int64   `parquet:"order_id"`
	CustomerID int64   `parquet:"customer_id"`
	Amount     float64 `parquet:"amount"`
	Status     string  `parquet:"status"`
}

func ordersParquet(year, month int) []byte {
	base := int64(year*100+month) * 1000
	rows := []order{
		{base + 1, 1, 120.50, "shipped"},
		{base + 2, 3, 45.00, "pending"},
		{base + 3, 2, 999.99, "shipped"},
		{base + 4, 5, 12.75, "cancelled"},
	}
	return writeParquet(rows)
}

type sale struct {
	SaleID  int64   `parquet:"sale_id"`
	Region  string  `parquet:"region"`
	Revenue float64 `parquet:"revenue"`
}

func salesParquet() []byte {
	rows := []sale{
		{1, "EMEA", 5000.0}, {2, "AMER", 8200.5}, {3, "APAC", 3100.25},
	}
	return writeParquet(rows)
}

func writeParquet[T any](rows []T) []byte {
	var buf bytes.Buffer
	w := pq.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
