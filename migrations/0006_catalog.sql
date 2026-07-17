-- Catalog: logical SQL tables registered from discovered datasets. A catalog
-- entry maps a name to a dataset storage location, which DuckDB exposes as a
-- view that reads directly from object storage (no ingest, no copy).

CREATE TABLE IF NOT EXISTS catalog_entries (
    id              VARCHAR PRIMARY KEY,
    name            VARCHAR NOT NULL,          -- the SQL table name (unique)
    dataset_id      VARCHAR NOT NULL,
    bucket_name     VARCHAR NOT NULL,
    root_path       VARCHAR NOT NULL,          -- prefix under the bucket
    format          VARCHAR NOT NULL,
    uri             VARCHAR,                   -- s3://bucket/root_path/
    glob_pattern            VARCHAR,                   -- resolved glob pattern (informational)
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    refresh_mode    VARCHAR NOT NULL DEFAULT 'manual',  -- manual | on_query
    schema_strategy VARCHAR NOT NULL DEFAULT 'union',   -- union | strict | latest
    partition_cols  VARCHAR,                   -- JSON array of hive partition column names
    description     VARCHAR,
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL,
    last_refresh_at TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_entries_name ON catalog_entries (name);
