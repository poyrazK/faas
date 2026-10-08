-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_recovery_failure_identity(progress jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE STRICT AS $$
SELECT jsonb_build_object('state',progress->'state','attempts',progress->'attempts',
 'updated_at',progress->'updated_at','failure_code',progress->'failure_code','retryable',progress->'retryable');
$$;
CREATE TABLE IF NOT EXISTS event_recovery_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 selection jsonb NOT NULL CHECK (jsonb_typeof(selection)='object'),
 rate_per_second integer NOT NULL CHECK (rate_per_second BETWEEN 1 AND 100),
 window_started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 window_count integer NOT NULL DEFAULT 0 CHECK (window_count >= 0 AND window_count <= rate_per_second),
 state text NOT NULL DEFAULT 'running' CHECK (state IN ('running','completed','cancelled')),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 completed_at timestamptz,
 CHECK ((state='running') = (completed_at IS NULL)),
 CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS event_recovery_jobs_due_idx ON event_recovery_jobs(next_attempt_at,id) WHERE state='running';
CREATE INDEX IF NOT EXISTS event_recovery_jobs_account_idx ON event_recovery_jobs(account_id,state);
CREATE TABLE IF NOT EXISTS event_recovery_items (
 job_id uuid NOT NULL REFERENCES event_recovery_jobs(id) ON DELETE CASCADE,
 position bigint NOT NULL CHECK (position > 0),
 outbox_id bigint NOT NULL,
 subscription_id text NOT NULL CHECK (subscription_id <> ''),
 event_source text NOT NULL,
 event_id text NOT NULL,
 event_type text NOT NULL,
 failed_at timestamptz NOT NULL,
 failure_code text NOT NULL,
 retryable boolean NOT NULL,
 expected_progress jsonb NOT NULL CHECK (jsonb_typeof(expected_progress)='object'),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','queued','skipped','cancelled')),
 reason text NOT NULL DEFAULT '' CHECK (reason IN ('','changed','receipt_expired','target_unavailable','cancelled','expired')),
 PRIMARY KEY (job_id,position),
 UNIQUE (job_id,outbox_id,subscription_id)
);
CREATE INDEX IF NOT EXISTS event_recovery_items_pending_idx ON event_recovery_items(job_id,position) WHERE state='pending';
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION event_recovery_failure_identity(jsonb);
DROP TABLE event_recovery_items;
DROP TABLE event_recovery_jobs;
