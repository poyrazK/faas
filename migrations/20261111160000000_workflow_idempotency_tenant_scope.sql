-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- Rebuild on retry if an interrupted concurrent build left an invalid index.
-- The legacy unique index still protects writes during this expand phase.
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_create_tenant_idempotency_idx;
CREATE UNIQUE INDEX CONCURRENTLY workflow_runs_create_tenant_idempotency_idx
    ON workflow_runs (app_id, workflow_name, platform_tenant_id, create_idempotency_key)
    NULLS NOT DISTINCT WHERE create_idempotency_key IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_create_tenant_idempotency_idx;
RESET lock_timeout;
