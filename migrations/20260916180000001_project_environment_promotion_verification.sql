-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    ADD COLUMN IF NOT EXISTS verification_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS verification_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS verification_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS verification_completed_at timestamptz;

DO $$
BEGIN
    ALTER TABLE project_environment_promotions
        ADD CONSTRAINT project_environment_promotions_verification_status_check
        CHECK (verification_status IN ('', 'pending', 'verifying', 'verified', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE project_environment_promotion_workloads
    ADD COLUMN IF NOT EXISTS verification_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS verification_error text NOT NULL DEFAULT '';

DO $$
BEGIN
    ALTER TABLE project_environment_promotion_workloads
        ADD CONSTRAINT project_environment_promotion_workloads_verification_status_check
        CHECK (verification_status IN ('', 'pending', 'verified', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_promotion_workloads
    DROP CONSTRAINT IF EXISTS project_environment_promotion_workloads_verification_status_check,
    DROP COLUMN IF EXISTS verification_error,
    DROP COLUMN IF EXISTS verification_status;

ALTER TABLE project_environment_promotions
    DROP CONSTRAINT IF EXISTS project_environment_promotions_verification_status_check,
    DROP COLUMN IF EXISTS verification_completed_at,
    DROP COLUMN IF EXISTS verification_started_at,
    DROP COLUMN IF EXISTS verification_error,
    DROP COLUMN IF EXISTS verification_status;
