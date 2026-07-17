-- Session token for temporary/STS credentials (AssumeRole, federated access,
-- SSO). Used alongside access_key and secret_key when present, blank otherwise.
ALTER TABLE connections ADD COLUMN IF NOT EXISTS session_token VARCHAR DEFAULT '';
UPDATE connections SET session_token = '' WHERE session_token IS NULL;
