-- Catalog tables saved from an ad-hoc multi-file selection are backed by an
-- explicit SELECT over a file list (read_parquet([...])), not a prefix glob, so
-- store that SELECT to rebuild the view on startup. Empty for dataset-backed
-- entries, which rebuild from their bucket/prefix as before.
ALTER TABLE catalog_entries ADD COLUMN IF NOT EXISTS select_sql VARCHAR DEFAULT '';
