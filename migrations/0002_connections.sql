-- Connection profiles: multiple S3/MinIO/AWS accounts. Exactly one is active at
-- a time and drives the explorer, crawler, and preview engine.

CREATE TABLE IF NOT EXISTS connections (
    id          VARCHAR PRIMARY KEY,
    name        VARCHAR NOT NULL,
    provider    VARCHAR NOT NULL DEFAULT 'minio',  -- minio | aws | s3
    endpoint    VARCHAR NOT NULL DEFAULT '',
    region      VARCHAR NOT NULL DEFAULT 'us-east-1',
    access_key  VARCHAR NOT NULL DEFAULT '',
    secret_key  VARCHAR NOT NULL DEFAULT '',
    use_ssl     BOOLEAN NOT NULL DEFAULT FALSE,
    is_active   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);

-- Generic key/value settings store.
CREATE TABLE IF NOT EXISTS settings (
    key        VARCHAR PRIMARY KEY,
    value      VARCHAR NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL
);
