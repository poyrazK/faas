-- filename: 20261027141113122_agent_run_workflow_steps_index.sql
-- migration-compat: expand
-- Build without queuing behind agent run writes. Replacing an interrupted
-- build also removes the invalid index PostgreSQL leaves.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- schedd's watcher reads finished workflow agent runs whose end it has not
-- delivered yet.
DROP INDEX CONCURRENTLY IF EXISTS agent_runs_workflow_finished_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS agent_runs_workflow_finished_idx ON agent_runs (finished_at, id)
    WHERE workflow_run_id IS NOT NULL AND finished_at IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS agent_runs_workflow_finished_idx;
RESET lock_timeout;
