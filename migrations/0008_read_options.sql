-- Per-dataset DuckDB reader options (filename, union_by_name, header,
-- sample_size, ignore_errors), stored as JSON so the registered view, preview,
-- and SQL prefill all read a dataset's files with the user's chosen options.

ALTER TABLE catalog_entries ADD COLUMN IF NOT EXISTS read_options VARCHAR DEFAULT '';
