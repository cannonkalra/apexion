# Architecture

Apexion is a single Go binary that bundles a web UI, a REST API, a concurrent
object-store crawler, a metadata catalog, and an embedded DuckDB query engine.
All state lives in one DuckDB file; the only external dependency is the object
store you point it at.

## Process layout

```
cmd/apexion            CLI entrypoint (serve · crawl · migrate · version)
   │
   └── internal/app    dependency-injection root; wires everything together
         ├── httpserver   chi router: web UI + REST API + static assets
         ├── ui           server-rendered pages (Templ + HTMX)
         ├── api          JSON REST handlers
         ├── explorer     object-store browsing (paginated listings)
         ├── crawler      concurrent discovery + schema inference
         ├── catalog      datasets → logical SQL tables (DuckDB views)
         ├── duckdb       preview + read-only query engine
         ├── connections  active S3 connection management
         ├── jobs         background job manager + scheduler
         ├── events       in-process event bus
         ├── storage      DuckDB-backed persistence + migrations
         ├── objstore     S3-compatible backends (S3 / MinIO / SeaweedFS / …)
         └── format       pluggable file-format readers (CSV / JSON / Parquet / …)
```

## Key ideas

**Everything is one binary.** The web assets, SQL migrations, and DuckDB driver
are all embedded. There is no separate database server, metastore, or worker
process to deploy.

**The catalog is a DuckDB file plus a DuckLake.** Buckets, datasets,
connections, and jobs are persisted to a single `.duckdb` database
(`storage.path`). Registered datasets become *views* over the underlying
object-store files, stored in a DuckLake catalog (`apexion.ducklake` by
default), so querying a table reads live data — nothing is copied — and any
DuckDB client that ATTACHes the lake sees the same tables. The `catalog_entries`
table keeps only what DuckLake doesn't: the link back to the dataset, read
options, and partition columns.

**Discovery is decoupled from formats.** The crawler walks objects and groups
them into datasets, but it never imports a concrete file-format reader directly.
Instead, readers self-register with the `features` registry via blank imports in
`internal/plugins`. Build tags decide which optional readers (Avro, ORC, Iceberg,
Delta) are compiled in, keeping the default binary — and its dependency tree —
small.

**Storage backends are pluggable.** Object-store backends implement a small
interface in `internal/objstore` and register by name (`s3`, `minio`, `aws`,
`seaweed`, …). The active connection is resolved at runtime and shared by the
explorer, crawler, and DuckDB engine.

**Reads over object storage are direct.** Preview and query both use DuckDB's
`httpfs` extension to read CSV/JSON/Parquet straight from S3 — no local staging.

## Request flow (a crawl)

1. A crawl is started from the CLI (`apexion crawl <bucket>`), the wizard, or the
   REST API.
2. The crawler lists objects concurrently (worker pool, rate limiting,
   checkpointing) and groups them into datasets, honoring Hive and positional
   partitions.
3. For each dataset it samples a representative file (up to `crawler.sample_bytes`)
   and infers a schema via the registered format reader.
4. Datasets and their metadata are persisted to the catalog, emitting events on
   the bus as it goes.
5. Registering a dataset creates a DuckDB view; the **Query** page then runs
   read-only SQL against it.

## Code generation

The UI is written in [Templ](https://templ.guide). The generated `*_templ.go`
files and the compiled Tailwind CSS (`assets/css/app.css`) are **committed**, so a
plain `go build ./cmd/apexion` works without the Templ or Tailwind toolchains. If
you change a `.templ` file or the CSS, regenerate with `make assets` and commit
the result.
