-- +goose Up
CREATE TABLE IF NOT EXISTS issue_team_slack_destinations (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 team_id uuid NOT NULL REFERENCES app_issue_teams(id) ON DELETE CASCADE,
 channel_id text NOT NULL CHECK(channel_id ~ '^[CG][A-Z0-9]{8,31}$'),
 token_sealed bytea NOT NULL CHECK(octet_length(token_sealed)>0),
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 next_send_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 claim_id uuid,
 claim_until timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((claim_id IS NULL)=(claim_until IS NULL)),
 UNIQUE(app_id,team_id)
);

CREATE TABLE IF NOT EXISTS issue_team_slack_deliveries (
 id uuid PRIMARY KEY,
 delivery_seq bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 channel_id text NOT NULL CHECK(channel_id ~ '^[CG][A-Z0-9]{8,31}$'),
 destination_id uuid NOT NULL REFERENCES issue_team_slack_destinations(id) ON DELETE CASCADE,
 destination_revision bigint NOT NULL CHECK(destination_revision>0),
 notification_id uuid NOT NULL REFERENCES issue_tracker_sync_notifications(id) ON DELETE CASCADE,
 incident_id uuid NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_flight','failed','succeeded','dead','canceled')),
 attempt integer NOT NULL DEFAULT 0 CHECK(attempt BETWEEN 0 AND 7),
 total_attempts bigint NOT NULL DEFAULT 0 CHECK(total_attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
 claim_id uuid,
 claim_until timestamptz,
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','destination_unavailable','delivery_expired','attempts_exhausted','token_unavailable','transport_error','rate_limited','slack_unavailable','slack_rejected','invalid_auth','channel_not_found','not_in_channel','missing_scope','thread_unavailable','invalid_response')),
 response_code integer NOT NULL DEFAULT 0 CHECK(response_code BETWEEN 0 AND 599),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((status='in_flight')=(claim_id IS NOT NULL) AND (claim_id IS NULL)=(claim_until IS NULL)),
 UNIQUE(destination_id,destination_revision,notification_id)
);

CREATE INDEX IF NOT EXISTS issue_team_slack_delivery_due ON issue_team_slack_deliveries(next_attempt_at,delivery_seq) WHERE status IN ('pending','failed','in_flight');

CREATE INDEX IF NOT EXISTS issue_team_slack_delivery_thread ON issue_team_slack_deliveries(destination_id,destination_revision,incident_id,delivery_seq);

CREATE TABLE IF NOT EXISTS issue_team_slack_threads (
 destination_id uuid NOT NULL REFERENCES issue_team_slack_destinations(id) ON DELETE CASCADE,
 destination_revision bigint NOT NULL CHECK(destination_revision>0),
 incident_id uuid NOT NULL,
 root_ts text NOT NULL DEFAULT '' CHECK(root_ts='' OR root_ts ~ '^[0-9]{1,20}\.[0-9]{1,10}$'),
 PRIMARY KEY(destination_id,destination_revision,incident_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Slack destination and delivery evidence must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
