-- filename: 20261007134643381_workflow_fair_dispatch.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-649: scheduling service order survives worker restart and history pruning.
CREATE TABLE workflow_dispatch_cursors (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE
        CHECK (app_id <> '00000000-0000-0000-0000-000000000000'),
    platform_tenant_id uuid REFERENCES platform_tenants(id) ON DELETE CASCADE
        CHECK (platform_tenant_id IS NULL OR platform_tenant_id <> '00000000-0000-0000-0000-000000000000'),
    scope_key text NOT NULL CHECK (
        (platform_tenant_id IS NULL AND scope_key IN ('app', 'unscoped')) OR
        (platform_tenant_id IS NOT NULL AND scope_key = platform_tenant_id::text)),
    last_claimed_at timestamptz NOT NULL CHECK (isfinite(last_claimed_at)),
    PRIMARY KEY (app_id, scope_key)
);
CREATE INDEX workflow_runs_dispatch_active_idx ON workflow_runs(app_id, platform_tenant_id, lease_until)
    WHERE status = 'running' AND operation_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX workflow_runs_dispatch_active_idx;
DROP TABLE workflow_dispatch_cursors;
-- +goose StatementEnd
