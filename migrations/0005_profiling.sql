-- Extended per-column profiling produced by internal/profiler over the whole
-- dataset (the crawler only fills the base columns from a small sample).
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS std_dev     DOUBLE;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS variance    DOUBLE;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS q25         DOUBLE;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS median      DOUBLE;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS q75         DOUBLE;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS min_length  BIGINT  DEFAULT 0;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS max_length  BIGINT  DEFAULT 0;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS avg_length  DOUBLE  DEFAULT 0;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS entropy     DOUBLE  DEFAULT 0;
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS top_values  VARCHAR DEFAULT '[]';
ALTER TABLE statistics ADD COLUMN IF NOT EXISTS profiled_at TIMESTAMP;

UPDATE statistics SET min_length = 0 WHERE min_length IS NULL;
UPDATE statistics SET max_length = 0 WHERE max_length IS NULL;
UPDATE statistics SET avg_length = 0 WHERE avg_length IS NULL;
UPDATE statistics SET entropy = 0 WHERE entropy IS NULL;
UPDATE statistics SET top_values = '[]' WHERE top_values IS NULL;

-- One profile row per dataset. Refreshed only when the data fingerprint changes.
CREATE TABLE IF NOT EXISTS dataset_profiles (
    dataset_id      VARCHAR PRIMARY KEY,
    fingerprint     VARCHAR NOT NULL DEFAULT '',
    row_count       BIGINT  NOT NULL DEFAULT 0,
    column_count    INTEGER NOT NULL DEFAULT 0,
    health_score    DOUBLE  NOT NULL DEFAULT 0,
    completeness    DOUBLE  NOT NULL DEFAULT 0,
    duplicate_ratio DOUBLE  NOT NULL DEFAULT 0,
    health          VARCHAR NOT NULL DEFAULT '{}',
    status          VARCHAR NOT NULL DEFAULT '',
    error           VARCHAR NOT NULL DEFAULT '',
    profiled_at     TIMESTAMP NOT NULL
);
