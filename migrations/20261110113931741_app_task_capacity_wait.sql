-- filename: 20261110113931741_app_task_capacity_wait.sql
-- ADR-20261029103512 amendment 3: a release task whose environment no compute
-- node had room for is requeued with a backoff instead of failing its
-- deployment. capacity_wait_started_at records the first such refusal, so the
-- wait is bounded from when the task first could not be placed rather than
-- from its creation; it is cleared when the task starts running. NULL, every
-- row today and every row an older schedd writes, means no wait is under way.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_tasks ADD COLUMN IF NOT EXISTS capacity_wait_started_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_tasks DROP COLUMN IF EXISTS capacity_wait_started_at;
