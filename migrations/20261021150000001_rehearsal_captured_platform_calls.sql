-- filename: 20261021150000001_rehearsal_captured_platform_calls.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Workflow starts, workflow events and realtime publishes an enrolled app
-- requests through the run-local platform API are captured here instead of
-- being performed (ADR-20261021150000). Rows are run evidence: they are
-- written before the run seals, never updated, and leave with the policy.
CREATE TABLE IF NOT EXISTS scenario_captured_platform_calls (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
 app_id uuid NOT NULL REFERENCES scenario_test_action_policies(app_id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('workflow_run', 'workflow_event', 'realtime_publish', 'realtime_signal')),
 -- A run member is named by workload so sibling runs compare; see the ADR
 -- for each kind's target form.
 target text NOT NULL CHECK (target ~ '^(workload|app|run|endpoint):' AND char_length(target) <= 512),
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 256),
 idempotency_key text CHECK (idempotency_key IS NULL OR char_length(idempotency_key) BETWEEN 1 AND 255),
 -- The 64 KiB bound applies to the request bytes; jsonb's text form adds
 -- separator whitespace, so the stored bound leaves room for it.
 message jsonb CHECK (message IS NULL OR octet_length(message::text) <= 262144),
 oversized boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (oversized = (message IS NULL))
);
CREATE INDEX IF NOT EXISTS scenario_captured_platform_calls_run_idx ON scenario_captured_platform_calls(account_id, run_id, created_at, id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_captured_platform_calls)
 THEN RAISE EXCEPTION 'captured rehearsal platform calls are retained run evidence'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE scenario_captured_platform_calls;
