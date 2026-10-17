-- +goose Up
-- +goose StatementBegin
ALTER TABLE hosting_releases ADD COLUMN IF NOT EXISTS last_activity_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(last_activity_at));
-- Existing releases receive a fresh idle window, never a retroactive expiry.
CREATE TABLE IF NOT EXISTS hosting_retention_policies (
 site_id uuid PRIMARY KEY REFERENCES hosting_sites(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision>0),
 enabled boolean NOT NULL DEFAULT false,
 pending_age_seconds bigint NOT NULL CHECK (pending_age_seconds BETWEEN 3600 AND 2592000),
 staged_age_seconds bigint NOT NULL CHECK (staged_age_seconds BETWEEN 3600 AND 2592000),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at))
);
CREATE INDEX IF NOT EXISTS hosting_retention_enabled_sites ON hosting_retention_policies(site_id) WHERE enabled;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM hosting_retention_policies WHERE enabled) THEN
  RAISE EXCEPTION 'Disable all hosting retention policies and drain retention workers before downgrade';
 END IF;
END $$;
DROP TABLE hosting_retention_policies;
ALTER TABLE hosting_releases DROP COLUMN last_activity_at;
-- +goose StatementEnd
