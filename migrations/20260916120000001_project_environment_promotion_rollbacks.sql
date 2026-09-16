-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    ADD COLUMN IF NOT EXISTS rollback_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rollback_idempotency_key text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rollback_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rollback_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS rollback_completed_at timestamptz;

DO $$
BEGIN
    ALTER TABLE project_environment_promotions
        ADD CONSTRAINT project_environment_promotions_rollback_status_check
        CHECK (rollback_status IN ('', 'rolling_back', 'rolled_back', 'rollback_failed'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE project_environment_promotions
        ADD CONSTRAINT project_environment_promotions_rollback_key_shape
        CHECK (length(rollback_idempotency_key) BETWEEN 0 AND 255);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE project_environment_promotion_workloads
    ADD COLUMN IF NOT EXISTS rollback_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS restored_target_deployment_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rollback_error text NOT NULL DEFAULT '';

DO $$
BEGIN
    ALTER TABLE project_environment_promotion_workloads
        ADD CONSTRAINT project_environment_promotion_workloads_rollback_status_check
        CHECK (rollback_status IN ('', 'pending', 'restored', 'cleared', 'unchanged', 'skipped', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_promotion_workloads
    DROP CONSTRAINT IF EXISTS project_environment_promotion_workloads_rollback_status_check,
    DROP COLUMN IF EXISTS rollback_error,
    DROP COLUMN IF EXISTS restored_target_deployment_id,
    DROP COLUMN IF EXISTS rollback_status;

ALTER TABLE project_environment_promotions
    DROP CONSTRAINT IF EXISTS project_environment_promotions_rollback_key_shape,
    DROP CONSTRAINT IF EXISTS project_environment_promotions_rollback_status_check,
    DROP COLUMN IF EXISTS rollback_completed_at,
    DROP COLUMN IF EXISTS rollback_started_at,
    DROP COLUMN IF EXISTS rollback_error,
    DROP COLUMN IF EXISTS rollback_idempotency_key,
    DROP COLUMN IF EXISTS rollback_status;
