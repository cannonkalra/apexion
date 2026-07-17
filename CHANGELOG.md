# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/cannonkalra/apexion/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/cannonkalra/apexion/releases/tag/v0.1.0
