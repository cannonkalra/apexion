-- Positional partition discovery: record how a dataset's layout was classified
-- and how many partition directory levels it has, so the query generator uses
-- stored metadata instead of reparsing object paths.

ALTER TABLE datasets ADD COLUMN IF NOT EXISTS partition_depth INTEGER DEFAULT 0;
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS discovery_strategy VARCHAR DEFAULT '';
