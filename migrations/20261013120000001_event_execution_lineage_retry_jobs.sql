-- +goose Up
CREATE TABLE IF NOT EXISTS event_execution_lineage_retry_jobs (
 subscription_id uuid NOT NULL REFERENCES event_execution_coverage(subscription_id) ON DELETE CASCADE,
 request_id uuid NOT NULL, account_id uuid NOT NULL, app_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('pending','completed')),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 retain_until timestamptz NOT NULL CHECK(retain_until>created_at),
 PRIMARY KEY(subscription_id,request_id)
);
CREATE INDEX IF NOT EXISTS event_execution_lineage_retry_jobs_pending_idx ON event_execution_lineage_retry_jobs(updated_at,subscription_id,request_id) WHERE state='pending';
CREATE INDEX IF NOT EXISTS event_execution_lineage_retry_jobs_expiry_idx ON event_execution_lineage_retry_jobs(retain_until);
-- +goose Down
DROP TABLE IF EXISTS event_execution_lineage_retry_jobs;
