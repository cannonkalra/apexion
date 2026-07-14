-- Force path-style S3 addressing (s3.host/bucket). Needed for legacy AWS bucket
-- names that are not DNS-compatible (uppercase letters, underscores, dots).
ALTER TABLE connections ADD COLUMN IF NOT EXISTS path_style BOOLEAN DEFAULT FALSE;
UPDATE connections SET path_style = FALSE WHERE path_style IS NULL;
