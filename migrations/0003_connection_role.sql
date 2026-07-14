-- AWS "role mode": use the instance/task/IRSA role via the AWS credential chain
-- instead of static access keys.
-- DuckDB's ALTER TABLE ADD COLUMN does not support NOT NULL, so we add the
-- column with a default and backfill any existing rows.
ALTER TABLE connections ADD COLUMN IF NOT EXISTS use_role BOOLEAN DEFAULT FALSE;
UPDATE connections SET use_role = FALSE WHERE use_role IS NULL;
