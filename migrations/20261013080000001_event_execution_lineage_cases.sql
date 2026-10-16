-- +goose Up
CREATE TABLE IF NOT EXISTS event_execution_lineage_cases (
 subscription_id uuid NOT NULL REFERENCES event_execution_coverage(subscription_id) ON DELETE CASCADE,
 outbox_id bigint NOT NULL CHECK(outbox_id>0), account_id uuid NOT NULL, app_id uuid NOT NULL,
 accepted_at timestamptz NOT NULL, root_invocation_id text NOT NULL DEFAULT '', root_created_at timestamptz,
 status text NOT NULL CHECK(status IN ('unresolved','conflict','repaired','already_associated')),
 reason text NOT NULL, revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), retry_count bigint NOT NULL DEFAULT 0 CHECK(retry_count>=0),
 recorded_at timestamptz NOT NULL, retain_until timestamptz NOT NULL CHECK(retain_until>recorded_at),
 PRIMARY KEY(subscription_id,outbox_id)
);
CREATE INDEX IF NOT EXISTS event_execution_lineage_cases_expiry_idx ON event_execution_lineage_cases(retain_until);
CREATE TABLE IF NOT EXISTS event_execution_lineage_retries (
 subscription_id uuid NOT NULL, outbox_id bigint NOT NULL, request_id uuid NOT NULL,
 expected_revision bigint NOT NULL CHECK(expected_revision>0), result jsonb NOT NULL,
 retain_until timestamptz NOT NULL,
 PRIMARY KEY(subscription_id,outbox_id,request_id),
 FOREIGN KEY(subscription_id,outbox_id) REFERENCES event_execution_lineage_cases(subscription_id,outbox_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS event_execution_lineage_retries_expiry_idx ON event_execution_lineage_retries(retain_until);
-- +goose Down
DROP TABLE IF EXISTS event_execution_lineage_retries;
DROP TABLE IF EXISTS event_execution_lineage_cases;
