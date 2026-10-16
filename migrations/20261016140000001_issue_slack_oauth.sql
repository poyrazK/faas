-- +goose Up
CREATE TABLE public.issue_slack_installations (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
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
CREATE INDEX issue_slack_installations_workspace ON public.issue_slack_installations(slack_app_id,workspace_id);
CREATE TABLE public.issue_slack_oauth_flows (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
 state_hash text NOT NULL UNIQUE CHECK(state_hash ~ '^[0-9a-f]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision>=0),
 phase text NOT NULL CHECK(phase IN ('authorizing','exchanging','choosing')),
 sealed bytea NOT NULL CHECK(octet_length(sealed) BETWEEN 1 AND 16384),
 expires_at timestamptz NOT NULL
);
CREATE INDEX issue_slack_oauth_flows_expiry ON public.issue_slack_oauth_flows(expires_at);
ALTER TABLE public.issue_team_slack_destinations ADD COLUMN oauth_revision bigint CHECK(oauth_revision>0);

ALTER TABLE public.issue_team_slack_destinations DROP CONSTRAINT issue_team_slack_destinations_token_sealed_check;
ALTER TABLE public.issue_team_slack_destinations ADD CONSTRAINT issue_team_slack_destinations_token_sealed_check CHECK(octet_length(token_sealed)>0 OR NOT enabled);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Slack OAuth installations are forward-only'; END $$;
-- +goose StatementEnd
