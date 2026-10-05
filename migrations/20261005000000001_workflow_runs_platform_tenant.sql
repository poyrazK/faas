-- +goose Up
ALTER TABLE workflow_runs
    ADD COLUMN IF NOT EXISTS platform_tenant_id uuid;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'workflow_runs'::regclass
           AND conname = 'workflow_runs_platform_tenant_id_fkey'
    ) THEN
        ALTER TABLE workflow_runs
            ADD CONSTRAINT workflow_runs_platform_tenant_id_fkey
            FOREIGN KEY (platform_tenant_id)
            REFERENCES platform_tenants(id)
            ON DELETE RESTRICT;
    END IF;
END
$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS workflow_runs_platform_tenant_idx
    ON workflow_runs (platform_tenant_id, created_at DESC)
    WHERE platform_tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX workflow_runs_platform_tenant_idx;
ALTER TABLE workflow_runs DROP COLUMN platform_tenant_id;
