-- +goose Up
CREATE TABLE IF NOT EXISTS issue_slack_interaction_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL CHECK(revision>0),
 workspace_id text NOT NULL CHECK(workspace_id ~ '^T[A-Z0-9]{8,31}$'),
 slack_app_id text NOT NULL CHECK(slack_app_id ~ '^A[A-Z0-9]{8,31}$'),
 secret_sealed bytea NOT NULL CHECK(octet_length(secret_sealed) BETWEEN 1 AND 8192)
);

CREATE TABLE IF NOT EXISTS issue_slack_action_messages (
 delivery_id uuid PRIMARY KEY REFERENCES issue_team_slack_deliveries(id) ON DELETE CASCADE,
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 response_id uuid,
 response_revision bigint NOT NULL CHECK(response_revision>=0),
 message_ts text NOT NULL DEFAULT '' CHECK(message_ts='' OR message_ts ~ '^[0-9]{1,20}\.[0-9]{1,10}$'),
 consumed_at timestamptz
);

CREATE TABLE IF NOT EXISTS issue_slack_identities (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 slack_user_id text NOT NULL CHECK(slack_user_id ~ '^[UW][A-Z0-9]{8,31}$'),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 mfa_enrolled_at timestamptz,
 mfa_required boolean NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,slack_user_id), UNIQUE(app_id,account_id)
);

CREATE INDEX IF NOT EXISTS issue_slack_identities_account ON issue_slack_identities(account_id);

CREATE TABLE IF NOT EXISTS issue_slack_link_challenges (
 token text PRIMARY KEY CHECK(token ~ '^[0-9a-f]{64}$'),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 delivery_id uuid NOT NULL REFERENCES issue_slack_action_messages(delivery_id) ON DELETE CASCADE,
 request_key text NOT NULL CHECK(request_key ~ '^[0-9a-f]{64}$'),
 slack_user_id text NOT NULL CHECK(slack_user_id ~ '^[UW][A-Z0-9]{8,31}$'),
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 UNIQUE(app_id,request_key)
);

CREATE INDEX IF NOT EXISTS issue_slack_link_challenges_expiry ON issue_slack_link_challenges(app_id,expires_at);
-- +goose StatementBegin


CREATE OR REPLACE FUNCTION issue_slack_revoke_mfa_links() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM issue_slack_identities WHERE account_id=NEW.id;
 RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER issue_slack_revoke_mfa_links AFTER UPDATE OF mfa_enrolled_at,mfa_required ON accounts
FOR EACH ROW WHEN (OLD.mfa_enrolled_at IS DISTINCT FROM NEW.mfa_enrolled_at OR OLD.mfa_required IS DISTINCT FROM NEW.mfa_required)
EXECUTE FUNCTION issue_slack_revoke_mfa_links();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Slack response linking is forward-only'; END $$;
-- +goose StatementEnd
