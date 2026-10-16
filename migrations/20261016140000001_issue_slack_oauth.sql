-- +goose Up
CREATE TABLE IF NOT EXISTS issue_slack_installations (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 status text NOT NULL CHECK(status IN ('connected','disconnected','revoked')),
 workspace_id text NOT NULL CHECK(workspace_id ~ '^T[A-Z0-9]{8,31}$'),
 workspace_name text NOT NULL CHECK(octet_length(workspace_name) BETWEEN 1 AND 256),
 slack_app_id text NOT NULL CHECK(slack_app_id ~ '^A[A-Z0-9]{8,31}$'),
 bot_user_id text NOT NULL CHECK(bot_user_id ~ '^[UW][A-Z0-9]{8,31}$'),
 token_sealed bytea NOT NULL CHECK(octet_length(token_sealed)<=8192),
 installed_at timestamptz NOT NULL,
 CHECK((status='connected')=(octet_length(token_sealed)>0))
);

CREATE INDEX IF NOT EXISTS issue_slack_installations_workspace ON issue_slack_installations(slack_app_id,workspace_id);

CREATE TABLE IF NOT EXISTS issue_slack_oauth_flows (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 state_hash text NOT NULL UNIQUE CHECK(state_hash ~ '^[0-9a-f]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision>=0),
 phase text NOT NULL CHECK(phase IN ('authorizing','exchanging','choosing')),
 sealed bytea NOT NULL CHECK(octet_length(sealed) BETWEEN 1 AND 16384),
 expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS issue_slack_oauth_flows_expiry ON issue_slack_oauth_flows(expires_at);

ALTER TABLE issue_team_slack_destinations ADD COLUMN IF NOT EXISTS oauth_revision bigint CHECK(oauth_revision>0);
-- +goose StatementBegin


DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_destinations'::regclass AND conname='issue_team_slack_destinations_token_sealed_check' AND position('NOT enabled' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_team_slack_destinations DROP CONSTRAINT IF EXISTS issue_team_slack_destinations_token_sealed_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_destinations'::regclass AND conname='issue_team_slack_destinations_token_sealed_check') THEN
  ALTER TABLE issue_team_slack_destinations ADD CONSTRAINT issue_team_slack_destinations_token_sealed_check CHECK(octet_length(token_sealed)>0 OR NOT enabled);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Slack OAuth installations are forward-only'; END $$;
-- +goose StatementEnd
