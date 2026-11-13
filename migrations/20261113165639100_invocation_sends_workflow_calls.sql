-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113120824 §2: a workflow's wf.call enqueues the callee's async
-- invocation with a send record naming the run and the call operation. When
-- the invocation is terminal, its own transaction settles that operation and
-- wakes the run. Both columns are set together or not at all.
ALTER TABLE invocation_sends ADD COLUMN IF NOT EXISTS workflow_run_id uuid;
ALTER TABLE invocation_sends ADD COLUMN IF NOT EXISTS workflow_op text;
-- NOT VALID: new writes are checked at once; 20261113165639200 validates.
ALTER TABLE invocation_sends ADD CONSTRAINT invocation_sends_workflow_call_check
 CHECK ((workflow_run_id IS NULL) = (workflow_op IS NULL)
  AND (workflow_op IS NULL OR length(workflow_op) BETWEEN 1 AND 160)) NOT VALID;

-- ADR-20261113120824 §3: a run a function started with start() records the
-- starting app, which alone may read it or send it events from its VM.
CREATE TABLE IF NOT EXISTS workflow_run_starts (
 run_id uuid PRIMARY KEY REFERENCES workflow_runs(id) ON DELETE CASCADE,
 starter_app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS workflow_run_starts_starter_idx ON workflow_run_starts(starter_app_id, created_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS workflow_run_starts;
ALTER TABLE invocation_sends DROP CONSTRAINT IF EXISTS invocation_sends_workflow_call_check;
ALTER TABLE invocation_sends DROP COLUMN IF EXISTS workflow_op;
ALTER TABLE invocation_sends DROP COLUMN IF EXISTS workflow_run_id;
