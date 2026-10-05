-- +goose Up
ALTER TABLE workflow_runs
    ADD COLUMN platform_tenant_id uuid REFERENCES platform_tenants(id) ON DELETE RESTRICT;

CREATE INDEX workflow_runs_platform_tenant_idx
    ON workflow_runs (platform_tenant_id, created_at DESC)
    WHERE platform_tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX workflow_runs_platform_tenant_idx;
ALTER TABLE workflow_runs DROP COLUMN platform_tenant_id;
