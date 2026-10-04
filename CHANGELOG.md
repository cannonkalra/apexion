# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.5] - 2026-10-04

### Changed
- **Catalog tables are stored in DuckLake.** Registered tables are now views in
  a DuckLake catalog (`data/apexion.ducklake` by default) instead of the query
  engine's in-memory database. They persist across restarts, and any DuckDB
  client can query them with `ATTACH 'ducklake:…' AS lake`. Existing tables are
  moved into the lake automatically on first start. Views still read files in
  place; nothing is copied, and DuckLake never takes ownership of your data.
  Configure with the new `ducklake` section; `ducklake.enabled: false` keeps
  the old in-memory behaviour.

- **Clearer names: selections, tables, and discovered folders.** A saved
  multi-file selection is now a *table* ("Save as table", previously "Save to
  Datasets", which actually saved into the Catalog). Crawled directories are
  *discovered folders*, under **Discovered** (previously **Datasets**). The
  Catalog shows each table's source: its selected files or its folder.

### Added
- **Monthly storage cost in the Explorer.** A new **Cost** column shows each
  file's estimated monthly S3 storage cost from its size and storage class, and
  each folder's (loaded lazily, up to its first 5,000 files); the folder
  summary shows the total. Rates are AWS's published on-demand list prices for
  the bucket's region, generated from the AWS Price List API
  (`go generate ./internal/pricing`), including minimum billable sizes for
  IA/Glacier Instant Retrieval and Glacier per-object overhead. Storage only:
  requests, retrieval, transfer, and Intelligent-Tiering monitoring are not
  included. Non-AWS stores show their S3-equivalent cost at us-east-1 rates.
- **macOS Intel (`darwin/amd64`) release binary**, built natively on CI.
- The Catalog page shows where the lake is stored, its latest snapshot, and the
  `ATTACH` statement to use it from DuckDB.
- The query page lists tables and views that other clients created in the lake.

## [0.0.4] - 2026-07-18

### Fixed
- **"Query in SQL" from a file preview.** The button linked to a non-existent
  `/sql` route (a 404 that did nothing) and the query console ignored the
  incoming statement, so the SQL editor never opened on the previewed file. It
  now opens the console pre-populated with a runnable `SELECT` over the object.

### Added
- **Reader-options bar with a "Read as" format override** on both the file
  preview page and the SQL editor: a format selector (CSV / TSV / JSON / JSON
  Lines / Parquet) plus Header, Union-by-name, and Ignore-errors toggles. An
  undetected or misnamed file — e.g. a gzipped `.log` that is really JSON Lines
  — can be pointed at the right DuckDB reader and previewed/queried with
  `ignore_errors`, without hand-editing SQL. Changing any option regenerates
  the preview and the prefilled SQL live.
- **CSV delimiter override** on every reader-options surface (preview page,
  single-file SQL editor, and the multi-file selection drawer): Auto / Comma /
  Semicolon / Pipe / Tab / Space. Rescues files where DuckDB's separator
  auto-detection fails, such as a one-line pipe-delimited `.csv.gz`.
- **Format + delimiter override in the multi-file selection drawer.** The SQL
  options drawer gains a "Read as" selector (alongside the existing Header
  toggle) that re-reads every selected file as one format, so a mixed or
  misdetected selection can be coerced — e.g. all files as CSV with a chosen
  delimiter.

### Changed
- **"Query in SQL" carries the chosen format and reader options into the
  editor** (via an out-of-band link update), and renders disabled for formats
  DuckDB cannot read directly (Avro / ORC / undetected) instead of generating a
  broken query. Previews of undetected formats now show a short "Read as…"
  guide instead of DuckDB's raw "preview not supported" error.

## [0.0.3] - 2026-07-18

### Added
- **Session token support** for connections — temporary/STS credentials
  (AssumeRole, SSO, federated access) are now honored across the S3 client and
  the DuckDB preview engine (migration `0010`).
- **Usage** section near the top of the README.

### Changed
- **Redesigned Connection Settings UI.** Enterprise-grade create-connection
  flow: a single compact empty state (never shown alongside the form), a
  sectioned form (*Connection details* / *Authentication* / *Advanced options*),
  an IAM-first authentication model with an info callout, storage-type-aware
  endpoint handling with a custom-endpoint toggle, live inline validation
  (Create stays disabled until valid), a submit loading state, smooth
  reveal/collapse animations, and full keyboard/ARIA accessibility.

## [0.0.2] - 2026-07-17

### Changed
- **No default connection.** Fresh installs now start with zero configured
  connections and a proper empty state instead of an auto-seeded `Default`
  MinIO connection. Existing saved connections are untouched, and no migration
  recreates a default.
- **IAM-first New connection form.** Defaults to AWS S3 with IAM (instance
  profile / role) authentication, region `us-east-1`, and endpoint
  `s3.amazonaws.com`; Access Key / Secret Key stay hidden and optional until
  explicit key-based auth is selected. Credentials are only validated when the
  chosen authentication method requires them.

### Docs
- Added **Running on EC2** and **Minimum IAM policy** sections to the README and
  reworked configuration docs to reflect the IAM-first, empty-state onboarding.

## [0.1.0] - 2026-07-17

First public release.

### Added
- Single-binary data lake exploration tool: crawl, catalog, preview, and query.
- **Explorer** — VS Code-style browser for S3-compatible object storage with
  cursor pagination, lazy folder summaries, prefix search, and sorting.
- **Crawler** — concurrent dataset discovery with full and incremental modes,
  worker pools, rate limiting, checkpointing, and resume.
- **Catalog** — register discovered datasets as logical SQL tables backed by
  DuckDB views, with Hive and positional partition handling.
- **Query** — read-only SQL scratchpad powered by an embedded DuckDB engine,
  reading directly from object storage.
- **Preview** — instant CSV/JSON/Parquet previews without downloading files.
- **Connections** — manage multiple S3 / MinIO / AWS connections and switch the
  active one at runtime; supports IAM instance-profile auth and path-style
  addressing.
- File-format readers for CSV/TSV, JSON/JSONL, and Parquet in the default build;
  optional Avro, ORC, Iceberg, and Delta Lake readers behind build tags.
- CLI commands: `serve`, `crawl`, `migrate`, and `version` (with embedded build
  metadata).
- Docker Compose stack (MinIO + sample-data seeder + app) for a one-command demo.
- Cross-platform release binaries for linux/amd64, linux/arm64, darwin/arm64, and
  windows/amd64, published with SHA256 checksums.

[Unreleased]: https://github.com/cannonkalra/apexion/compare/v0.0.5...HEAD
[0.0.5]: https://github.com/cannonkalra/apexion/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/cannonkalra/apexion/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/cannonkalra/apexion/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/cannonkalra/apexion/compare/v0.0.1...v0.0.2
[0.1.0]: https://github.com/cannonkalra/apexion/releases/tag/v0.1.0
[0.0.1]: https://github.com/cannonkalra/apexion/releases/tag/v0.0.1
