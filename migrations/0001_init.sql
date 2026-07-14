-- Apexion catalog schema (DuckDB).
-- Fully normalized: one table per entity. Arrays/maps are stored as JSON text
-- to keep the database/sql binding portable and simple.

CREATE TABLE IF NOT EXISTS buckets (
    id             VARCHAR PRIMARY KEY,
    name           VARCHAR NOT NULL,
    endpoint       VARCHAR NOT NULL DEFAULT '',
    region         VARCHAR NOT NULL DEFAULT '',
    object_count   BIGINT  NOT NULL DEFAULT 0,
    total_size     BIGINT  NOT NULL DEFAULT 0,
    dataset_count  BIGINT  NOT NULL DEFAULT 0,
    schedule       VARCHAR NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL,
    last_crawl_at  TIMESTAMP
);

CREATE TABLE IF NOT EXISTS datasets (
    id             VARCHAR PRIMARY KEY,
    bucket_id      VARCHAR NOT NULL,
    bucket_name    VARCHAR NOT NULL DEFAULT '',
    name           VARCHAR NOT NULL,
    path           VARCHAR NOT NULL DEFAULT '',
    format         VARCHAR NOT NULL DEFAULT 'unknown',
    compression    VARCHAR NOT NULL DEFAULT 'none',
    file_count     BIGINT  NOT NULL DEFAULT 0,
    total_size     BIGINT  NOT NULL DEFAULT 0,
    row_count      BIGINT  NOT NULL DEFAULT 0,
    partition_keys VARCHAR NOT NULL DEFAULT '[]',
    schema_id      VARCHAR NOT NULL DEFAULT '',
    description    VARCHAR NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL,
    last_scan_at   TIMESTAMP
);

CREATE TABLE IF NOT EXISTS objects (
    id             VARCHAR PRIMARY KEY,
    bucket_id      VARCHAR NOT NULL,
    bucket_name    VARCHAR NOT NULL DEFAULT '',
    key            VARCHAR NOT NULL,
    etag           VARCHAR NOT NULL DEFAULT '',
    version_id     VARCHAR NOT NULL DEFAULT '',
    size           BIGINT  NOT NULL DEFAULT 0,
    format         VARCHAR NOT NULL DEFAULT 'unknown',
    compression    VARCHAR NOT NULL DEFAULT 'none',
    dataset_id     VARCHAR NOT NULL DEFAULT '',
    is_hidden      BOOLEAN NOT NULL DEFAULT FALSE,
    metadata_hash  VARCHAR NOT NULL DEFAULT '',
    storage_class  VARCHAR NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    modified       TIMESTAMP NOT NULL,
    discovered_at  TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS schemas (
    id           VARCHAR PRIMARY KEY,
    dataset_id   VARCHAR NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1,
    fingerprint  VARCHAR NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS columns (
    id             VARCHAR PRIMARY KEY,
    schema_id      VARCHAR NOT NULL,
    name           VARCHAR NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    data_type      VARCHAR NOT NULL DEFAULT 'unknown',
    physical_type  VARCHAR NOT NULL DEFAULT '',
    nullable       BOOLEAN NOT NULL DEFAULT TRUE,
    semantic_type  VARCHAR NOT NULL DEFAULT '',
    is_partition   BOOLEAN NOT NULL DEFAULT FALSE,
    description    VARCHAR NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS statistics (
    id             VARCHAR PRIMARY KEY,
    column_id      VARCHAR NOT NULL,
    null_count     BIGINT  NOT NULL DEFAULT 0,
    distinct_count BIGINT  NOT NULL DEFAULT 0,
    min_value      VARCHAR NOT NULL DEFAULT '',
    max_value      VARCHAR NOT NULL DEFAULT '',
    mean_value     DOUBLE,
    sample_count   BIGINT  NOT NULL DEFAULT 0,
    completeness   DOUBLE  NOT NULL DEFAULT 0,
    uniqueness     DOUBLE  NOT NULL DEFAULT 0,
    sample_values  VARCHAR NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS partitions (
    id          VARCHAR PRIMARY KEY,
    dataset_id  VARCHAR NOT NULL,
    path        VARCHAR NOT NULL,
    values      VARCHAR NOT NULL DEFAULT '{}',
    file_count  BIGINT  NOT NULL DEFAULT 0,
    size        BIGINT  NOT NULL DEFAULT 0,
    row_count   BIGINT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS lineage_nodes (
    id          VARCHAR PRIMARY KEY,
    kind        VARCHAR NOT NULL,
    ref_id      VARCHAR NOT NULL,
    label       VARCHAR NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS lineage_edges (
    id          VARCHAR PRIMARY KEY,
    from_id     VARCHAR NOT NULL,
    to_id       VARCHAR NOT NULL,
    relation    VARCHAR NOT NULL DEFAULT 'contains',
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS crawler_runs (
    id              VARCHAR PRIMARY KEY,
    bucket_id       VARCHAR NOT NULL,
    bucket_name     VARCHAR NOT NULL DEFAULT '',
    mode            VARCHAR NOT NULL DEFAULT 'full',
    trigger         VARCHAR NOT NULL DEFAULT 'manual',
    status          VARCHAR NOT NULL DEFAULT 'queued',
    objects_scanned BIGINT  NOT NULL DEFAULT 0,
    objects_new     BIGINT  NOT NULL DEFAULT 0,
    objects_changed BIGINT  NOT NULL DEFAULT 0,
    datasets_found  BIGINT  NOT NULL DEFAULT 0,
    bytes_scanned   BIGINT  NOT NULL DEFAULT 0,
    checkpoint      VARCHAR NOT NULL DEFAULT '',
    error           VARCHAR NOT NULL DEFAULT '',
    started_at      TIMESTAMP NOT NULL,
    finished_at     TIMESTAMP
);

CREATE TABLE IF NOT EXISTS inference_runs (
    id            VARCHAR PRIMARY KEY,
    dataset_id    VARCHAR NOT NULL,
    dataset_name  VARCHAR NOT NULL DEFAULT '',
    status        VARCHAR NOT NULL DEFAULT 'queued',
    sample_rows   BIGINT  NOT NULL DEFAULT 0,
    quality_score DOUBLE  NOT NULL DEFAULT 0,
    findings      VARCHAR NOT NULL DEFAULT '{}',
    error         VARCHAR NOT NULL DEFAULT '',
    started_at    TIMESTAMP NOT NULL,
    finished_at   TIMESTAMP
);

CREATE TABLE IF NOT EXISTS data_samples (
    id          VARCHAR PRIMARY KEY,
    dataset_id  VARCHAR NOT NULL,
    rows_json   VARCHAR NOT NULL DEFAULT '[]',
    row_count   INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
    id          VARCHAR PRIMARY KEY,
    type        VARCHAR NOT NULL,
    status      VARCHAR NOT NULL DEFAULT 'queued',
    ref_id      VARCHAR NOT NULL DEFAULT '',
    label       VARCHAR NOT NULL DEFAULT '',
    progress    DOUBLE  NOT NULL DEFAULT 0,
    message     VARCHAR NOT NULL DEFAULT '',
    error       VARCHAR NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL,
    started_at  TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS events (
    id          VARCHAR PRIMARY KEY,
    type        VARCHAR NOT NULL,
    subject     VARCHAR NOT NULL DEFAULT '',
    payload     VARCHAR NOT NULL DEFAULT '{}',
    created_at  TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_objects_bucket   ON objects(bucket_id);
CREATE INDEX IF NOT EXISTS idx_objects_dataset  ON objects(dataset_id);
CREATE INDEX IF NOT EXISTS idx_objects_key      ON objects(key);
CREATE INDEX IF NOT EXISTS idx_datasets_bucket  ON datasets(bucket_id);
CREATE INDEX IF NOT EXISTS idx_schemas_dataset  ON schemas(dataset_id);
CREATE INDEX IF NOT EXISTS idx_columns_schema   ON columns(schema_id);
CREATE INDEX IF NOT EXISTS idx_stats_column     ON statistics(column_id);
CREATE INDEX IF NOT EXISTS idx_parts_dataset    ON partitions(dataset_id);
CREATE INDEX IF NOT EXISTS idx_runs_bucket      ON crawler_runs(bucket_id);
CREATE INDEX IF NOT EXISTS idx_infruns_dataset  ON inference_runs(dataset_id);
CREATE INDEX IF NOT EXISTS idx_events_type      ON events(type);
CREATE INDEX IF NOT EXISTS idx_edges_from       ON lineage_edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to         ON lineage_edges(to_id);
