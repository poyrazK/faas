-- +goose Up
-- ADR-531: retire dispatched independent projects only after authenticated
-- provider deletion observation. Preserve unknown outcomes and input holds.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_copy_targets'::regclass AND attname='deletion_started_at' AND NOT attisdropped) THEN
        ALTER TABLE project_environment_clone_postgres_copy_targets
            DROP CONSTRAINT project_environment_clone_postgres_copy_targets_state_check,
            DROP CONSTRAINT project_environment_clone_postgres_copy_targets_check1,
            ADD COLUMN IF NOT EXISTS deletion_started_at timestamptz,
            ADD COLUMN IF NOT EXISTS deletion_observed_at timestamptz,
            ADD CHECK (state IN ('reserved','requested','preparing','prepared','deleting','retired')),
            ADD CONSTRAINT clone_copy_target_lifecycle_shape CHECK (
                (state='reserved' AND request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL AND deletion_started_at IS NULL AND deletion_observed_at IS NULL)
                OR (state='requested' AND request_started_at IS NOT NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL AND deletion_started_at IS NULL AND deletion_observed_at IS NULL)
                OR (state='preparing' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NULL AND retired_at IS NULL AND deletion_started_at IS NULL AND deletion_observed_at IS NULL)
                OR (state='prepared' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NOT NULL AND retired_at IS NULL AND deletion_started_at IS NULL AND deletion_observed_at IS NULL)
                OR (state='deleting' AND request_started_at IS NOT NULL AND retired_at IS NULL AND deletion_started_at IS NOT NULL AND deletion_observed_at IS NULL AND
                    ((provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL)
                        OR (provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL)))
                OR (state='retired' AND retired_at IS NOT NULL AND
                    ((request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND deletion_started_at IS NULL AND deletion_observed_at IS NULL)
                        OR (request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND deletion_started_at IS NOT NULL AND deletion_observed_at IS NOT NULL)))),
            ADD CONSTRAINT clone_copy_target_deletion_order CHECK (deletion_observed_at IS NULL OR (deletion_observed_at>=deletion_started_at AND deletion_observed_at>=provider_created_at AND retired_at>=deletion_observed_at));
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_clone_postgres_copy_targets
    ADD CONSTRAINT copy_target_cleanup_down_no_retirements CHECK (deletion_started_at IS NULL AND deletion_observed_at IS NULL);
ALTER TABLE project_environment_clone_postgres_copy_targets
    DROP CONSTRAINT project_environment_clone_postgres_copy_targets_state_check,
    DROP CONSTRAINT clone_copy_target_lifecycle_shape,
    DROP CONSTRAINT clone_copy_target_deletion_order,
    DROP CONSTRAINT copy_target_cleanup_down_no_retirements,
    DROP COLUMN deletion_started_at,
    DROP COLUMN deletion_observed_at,
    ADD CHECK (state IN ('reserved','requested','preparing','prepared','retired')),
    ADD CHECK ((state='reserved' AND request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='requested' AND request_started_at IS NOT NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='preparing' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='prepared' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NOT NULL AND retired_at IS NULL)
        OR (state='retired' AND request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NOT NULL));
