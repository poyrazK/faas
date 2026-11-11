-- migration-compat: contract
-- +goose NO TRANSACTION

-- +goose Up
-- Apply in the full release after every API replica uses tenant-scoped lookup.
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_create_idempotency_idx;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- Refuse a downgrade if different tenants have since reused the same key.
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_create_idempotency_idx;
CREATE UNIQUE INDEX CONCURRENTLY workflow_runs_create_idempotency_idx
    ON workflow_runs (app_id, workflow_name, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;
RESET lock_timeout;
