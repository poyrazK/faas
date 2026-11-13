-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113120824 §2: a workflow's wf.call enqueues the callee's async
-- invocation with a send record naming the run and the call operation. When
-- the invocation is terminal, its own transaction settles that operation and
-- wakes the run. Both columns are set together or not at all.
ALTER TABLE invocation_sends ADD COLUMN IF NOT EXISTS workflow_run_id uuid;
ALTER TABLE invocation_sends ADD COLUMN IF NOT EXISTS workflow_op text;
ALTER TABLE invocation_sends DROP CONSTRAINT IF EXISTS invocation_sends_workflow_call_check;
ALTER TABLE invocation_sends ADD CONSTRAINT invocation_sends_workflow_call_check
 CHECK ((workflow_run_id IS NULL) = (workflow_op IS NULL)
  AND (workflow_op IS NULL OR length(workflow_op) BETWEEN 1 AND 160));

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE invocation_sends DROP CONSTRAINT IF EXISTS invocation_sends_workflow_call_check;
ALTER TABLE invocation_sends DROP COLUMN IF EXISTS workflow_op;
ALTER TABLE invocation_sends DROP COLUMN IF EXISTS workflow_run_id;
