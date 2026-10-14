-- +goose Up
ALTER TABLE public.issue_tracker_linear_connections
 ADD COLUMN auth_method text NOT NULL DEFAULT 'api_key' CHECK(auth_method IN ('api_key','oauth')),
 ADD COLUMN oauth_status text NOT NULL DEFAULT '' CHECK(oauth_status IN ('','ready','refreshing','reconnect_required','revoking')),
 ADD COLUMN access_token_expires_at timestamptz,
 ADD COLUMN credential_generation bigint NOT NULL DEFAULT 1 CHECK(credential_generation>0),
 ADD COLUMN refresh_claim_id uuid,
 ADD COLUMN refresh_claim_until timestamptz,
 ADD COLUMN refresh_started_at timestamptz,
 ADD CONSTRAINT issue_tracker_linear_oauth_shape CHECK((auth_method='api_key' AND oauth_status='' AND access_token_expires_at IS NULL) OR (auth_method='oauth' AND connected AND oauth_status<>'' AND access_token_expires_at IS NOT NULL));
CREATE TABLE public.issue_tracker_linear_oauth_flows (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
 issue_id uuid NOT NULL,
 state_hash text UNIQUE NOT NULL CHECK(state_hash ~ '^[a-f0-9]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision>=0),
 phase text NOT NULL CHECK(phase IN ('authorizing','exchanging','choosing','consuming')),
 sealed_state bytea NOT NULL CHECK(octet_length(sealed_state)>0),
 expires_at timestamptz NOT NULL
);
CREATE INDEX issue_tracker_linear_oauth_flows_expiry_idx ON public.issue_tracker_linear_oauth_flows(expires_at);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_linear_oauth_flows) OR EXISTS(SELECT 1 FROM issue_tracker_linear_connections WHERE auth_method='oauth') THEN
 RAISE EXCEPTION 'disconnect OAuth connections and preserve pending flows before rollback'; END IF;
END $$;
DROP TABLE public.issue_tracker_linear_oauth_flows;
ALTER TABLE public.issue_tracker_linear_connections DROP CONSTRAINT issue_tracker_linear_oauth_shape,
 DROP COLUMN auth_method,DROP COLUMN oauth_status,DROP COLUMN access_token_expires_at,DROP COLUMN credential_generation,DROP COLUMN refresh_claim_id,DROP COLUMN refresh_claim_until,DROP COLUMN refresh_started_at;
-- +goose StatementEnd
