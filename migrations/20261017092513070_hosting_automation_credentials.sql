-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS hosting_automation_credentials (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE CASCADE,
 token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
 label text NOT NULL CHECK (octet_length(label) BETWEEN 1 AND 64),
 operations text[] NOT NULL CHECK (cardinality(operations) BETWEEN 1 AND 2 AND operations <@ ARRAY['stage','preview']::text[]),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at>created_at)
);
CREATE INDEX IF NOT EXISTS hosting_automation_credentials_site ON hosting_automation_credentials(site_id);
CREATE TABLE IF NOT EXISTS hosting_ci_runs (
 site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE CASCADE,
 id uuid NOT NULL,
 release_id uuid NOT NULL,
 preview_id uuid NOT NULL UNIQUE,
 preview_created boolean NOT NULL DEFAULT false,
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 preview_expires_at timestamptz NOT NULL CHECK (isfinite(preview_expires_at) AND preview_expires_at>created_at),
 PRIMARY KEY(site_id,id)
);
-- Run IDs intentionally survive release/preview GC, preventing late retries from
-- allocating a second resource after receipts or resource catalog rows disappear.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM hosting_automation_credentials) OR EXISTS(SELECT 1 FROM hosting_ci_runs) THEN
  RAISE EXCEPTION 'Revoke credentials and preserve CI run identities until retiring the isolated pilot before downgrade';
 END IF;
END $$;
DROP TABLE hosting_ci_runs;
DROP TABLE hosting_automation_credentials;
-- +goose StatementEnd
