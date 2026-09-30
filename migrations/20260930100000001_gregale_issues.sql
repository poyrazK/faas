-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_issues (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment text NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
 grouping_version integer NOT NULL CHECK (grouping_version > 0),
 title text NOT NULL CHECK (octet_length(title) <= 2048),
 state text NOT NULL DEFAULT 'open' CHECK (state IN ('open','resolved','ignored')),
 assignee_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 first_seen_at timestamptz NOT NULL,
 last_seen_at timestamptz NOT NULL,
 event_count bigint NOT NULL DEFAULT 0 CHECK (event_count >= 0),
 regression_count bigint NOT NULL DEFAULT 0 CHECK (regression_count >= 0),
 resolved_at timestamptz,
 fixed_deployment_id uuid,
 fixed_deployment_created_at timestamptz,
 ignored_until timestamptz,
 UNIQUE(app_id,environment,grouping_version,fingerprint)
);
CREATE INDEX app_issues_list_idx ON app_issues(app_id,last_seen_at DESC,id DESC);
CREATE TABLE issue_ingest_tokens (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 environment text NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
 token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX issue_ingest_tokens_app_idx ON issue_ingest_tokens(app_id,expires_at);
CREATE TABLE issue_events (
 id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL,
 event_id uuid NOT NULL,
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 payload_hash text NOT NULL CHECK (payload_hash ~ '^[a-f0-9]{64}$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 65536),
 occurred_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 attribution_checked_at timestamptz NOT NULL DEFAULT now(),
 verified_consumer_id uuid,
 verified_platform_tenant_id uuid,
 PRIMARY KEY(app_id,deployment_id,event_id)
);
CREATE INDEX issue_events_list_idx ON issue_events(issue_id,occurred_at DESC,id DESC);
CREATE INDEX issue_events_attribution_idx ON issue_events(attribution_checked_at) WHERE verified_consumer_id IS NULL AND verified_platform_tenant_id IS NULL;
CREATE INDEX issue_events_retention_idx ON issue_events(app_id,received_at);
CREATE TABLE issue_releases (
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL,
 commit_sha text NOT NULL,
 image_digest text NOT NULL,
 event_count bigint NOT NULL CHECK(event_count > 0),
 first_seen_at timestamptz NOT NULL,
 last_seen_at timestamptz NOT NULL,
 PRIMARY KEY(issue_id,deployment_id)
);
CREATE TABLE issue_activity (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 action text NOT NULL CHECK(action IN ('created','assigned','resolved','reopened','ignored','regressed')),
 actor_account_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(details) = 'object')
);
CREATE INDEX issue_activity_list_idx ON issue_activity(issue_id,created_at DESC,id DESC);
CREATE TABLE issue_resolutions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 fixed_deployment_id uuid NOT NULL,
 resolved_at timestamptz NOT NULL,
 actor_account_id uuid NOT NULL
);
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
 CHECK(event IN ('usage_statement.finalized','app.parked','app.woken','issue.created','issue.assigned','issue.resolved','issue.reopened','issue.ignored','issue.regressed'));
-- +goose StatementEnd

-- +goose Down
-- Durable triage history is not dropped by automated rollback.
SELECT 1;
