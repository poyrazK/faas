-- filename: 20261001070000002_scoped_queue_demand.sql

-- +goose Up
-- Queue demand is sampled per captured environment on every scheduler tick.
-- Completed history is excluded from this index and the aggregate query.
CREATE INDEX IF NOT EXISTS invocations_scoped_queue_demand_idx
ON invocations (app_id, deployment_scope, queue_name)
INCLUDE (state, lease_expires_at, created_at)
WHERE source='queue' AND state IN ('pending','dispatching','dead_letter');

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
