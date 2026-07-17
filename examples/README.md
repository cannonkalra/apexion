# Examples

Practical ways to drive Apexion. All of these assume the server is running on
`http://localhost:8080` (`apexion serve`, or `make up` for the full Docker demo).

## 1. The one-command demo

The fastest way to try everything end to end — MinIO, a seeder that uploads
sample datasets (CSV with PII, JSONL, Parquet, a Hive-partitioned table), and the
app:

```bash
make up
open http://localhost:8080   # or just visit it in a browser
```

The seeded data lands in a bucket named `warehouse`. Crawl it, preview the files,
register a dataset, and query it from the **Query** page.

## 2. Crawl from the CLI

```bash
# Point Apexion at your store (or use the compose defaults).
export APEXION_MINIO_ENDPOINT=localhost:9000
export APEXION_MINIO_ACCESS_KEY=minioadmin
export APEXION_MINIO_SECRET_KEY=minioadmin

apexion crawl warehouse                 # full crawl
apexion crawl warehouse --mode incremental
```

## 3. Drive the REST API

```bash
# List buckets known to the catalog.
curl -s http://localhost:8080/api/buckets | jq

# Kick off a crawl of a bucket.
curl -s -X POST http://localhost:8080/api/buckets/warehouse/crawl | jq

# List discovered datasets.
curl -s http://localhost:8080/api/datasets | jq

# Run a read-only SQL query over a registered table.
curl -s -X POST http://localhost:8080/api/query \
  -H 'content-type: application/json' \
  -d '{"sql":"SELECT * FROM customers LIMIT 10"}' | jq
```

## 4. Run against AWS S3 on EC2

No static keys required — use the instance profile:

```bash
export APEXION_MINIO_ENDPOINT=s3.us-east-1.amazonaws.com
export APEXION_MINIO_REGION=us-east-1
export APEXION_MINIO_USE_SSL=true
apexion serve
```

Then open **Settings → Connections**, enable role-based auth, and activate the
connection. See [../docs/configuration.md](../docs/configuration.md) for every
available setting.
