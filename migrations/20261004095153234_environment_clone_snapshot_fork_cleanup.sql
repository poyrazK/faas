-- +goose Up
-- ADR-531: preserve native fork retirement evidence before releasing quota or
-- discarding the source snapshot. Unknown creation outcomes remain discoverable.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_snapshot_restores'::regclass AND attname='deletion_started_at' AND NOT attisdropped) THEN
        ALTER TABLE project_environment_clone_postgres_snapshot_restores
            DROP CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check,
            DROP CONSTRAINT project_environment_clone_postgres_snapshot_restores_check1,
            ADD COLUMN IF NOT EXISTS deletion_started_at timestamptz,
            ADD COLUMN IF NOT EXISTS deleted_at timestamptz,
            ADD COLUMN IF NOT EXISTS delete_operation_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
            ADD CHECK (state IN ('reserved','requested','restoring','restored','deleting','deleted')),
            ADD CHECK (jsonb_typeof(delete_operation_ids)='array'),
            ADD CHECK ((state IN ('deleting','deleted'))=(deletion_started_at IS NOT NULL)),
            ADD CHECK ((state='deleted')=(deleted_at IS NOT NULL)),
            ADD CHECK (deleted_at IS NULL OR deleted_at>=deletion_started_at),
            ADD CHECK ((state='reserved' AND request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                OR (state='requested' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                OR (state='restoring' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                OR (state='restored' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NOT NULL AND delete_operation_ids='[]'::jsonb)
                OR (state IN ('deleting','deleted') AND (
                    (request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                    OR (request_started_at IS NOT NULL AND ((target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                        OR (target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL)))))),
            ADD CHECK (state<>'deleted' OR request_started_at IS NULL OR (target_provider_resource_id IS NOT NULL AND jsonb_array_length(delete_operation_ids)>0));
    END IF;
END;
$$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS project_environment_clone_postgres_fork_account_quota
    ON project_environment_clone_postgres_snapshot_restores(account_id) WHERE state<>'deleted';

-- +goose Down
-- Retired rows cannot be represented in the old protocol; refuse to erase them.
ALTER TABLE project_environment_clone_postgres_snapshot_restores
    ADD CONSTRAINT fork_cleanup_down_no_retirements CHECK (state NOT IN ('deleting','deleted'));
DROP INDEX project_environment_clone_postgres_fork_account_quota;
ALTER TABLE project_environment_clone_postgres_snapshot_restores
    DROP COLUMN deletion_started_at,
    DROP COLUMN deleted_at,
    DROP COLUMN delete_operation_ids,
    DROP CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check,
    DROP CONSTRAINT fork_cleanup_down_no_retirements,
    ADD CHECK (state IN ('reserved','requested','restoring','restored')),
    ADD CHECK ((state='reserved' AND request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL)
        OR (state='requested' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL)
        OR (state='restoring' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NULL)
        OR (state='restored' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NOT NULL));
