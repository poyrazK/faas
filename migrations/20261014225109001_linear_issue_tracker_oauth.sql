-- +goose Up
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS auth_method text NOT NULL DEFAULT 'api_key' CHECK(auth_method IN ('api_key','oauth'));
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS oauth_status text NOT NULL DEFAULT '' CHECK(oauth_status IN ('','ready','refreshing','reconnect_required','revoking'));
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS access_token_expires_at timestamptz;
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS credential_generation bigint NOT NULL DEFAULT 1 CHECK(credential_generation>0);
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS refresh_claim_id uuid;
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS refresh_claim_until timestamptz;
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS refresh_started_at timestamptz;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_linear_connections'::regclass AND conname='issue_tracker_linear_oauth_shape') THEN
  ALTER TABLE issue_tracker_linear_connections ADD CONSTRAINT issue_tracker_linear_oauth_shape CHECK((auth_method='api_key' AND oauth_status='' AND access_token_expires_at IS NULL) OR (auth_method='oauth' AND connected AND oauth_status<>'' AND access_token_expires_at IS NOT NULL));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS issue_tracker_linear_oauth_flows (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 issue_id uuid NOT NULL,
 state_hash text UNIQUE NOT NULL CHECK(state_hash ~ '^[a-f0-9]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision>=0),
 phase text NOT NULL CHECK(phase IN ('authorizing','exchanging','choosing','consuming')),
 sealed_state bytea NOT NULL CHECK(octet_length(sealed_state)>0),
 expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS issue_tracker_linear_oauth_flows_expiry_idx ON issue_tracker_linear_oauth_flows(expires_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_linear_oauth_flows) OR EXISTS(SELECT 1 FROM issue_tracker_linear_connections WHERE auth_method='oauth') THEN
 RAISE EXCEPTION 'disconnect OAuth connections and preserve pending flows before rollback'; END IF;
END $$;
DROP TABLE issue_tracker_linear_oauth_flows;
ALTER TABLE issue_tracker_linear_connections DROP CONSTRAINT issue_tracker_linear_oauth_shape,
 DROP COLUMN auth_method,DROP COLUMN oauth_status,DROP COLUMN access_token_expires_at,DROP COLUMN credential_generation,DROP COLUMN refresh_claim_id,DROP COLUMN refresh_claim_until,DROP COLUMN refresh_started_at;
-- +goose StatementEnd
