-- migration-compat: expand
-- filename: 20261104223908496_tenant_schedule_invalidation.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_schedule_invalidations (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    invalidated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(app_id,name)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM workflow_automation_schedule_invalidations) THEN
        RAISE EXCEPTION 'cannot remove tenant schedule invalidations while they are in use';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE IF EXISTS workflow_automation_schedule_invalidations;
