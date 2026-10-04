-- +goose Up
-- ADR-531: transfer the native owner and quota to a private catalogue row.
-- Adoption does not establish SQL isolation, readiness or publication.
ALTER TABLE project_environment_clone_postgres_snapshot_restores
    DROP CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check,
    DROP CONSTRAINT project_environment_clone_postgres_snapshot_restores_check5,
    ADD COLUMN adopted_database_id uuid UNIQUE REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
    ADD COLUMN adopted_at timestamptz,
    ADD CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check
        CHECK (state IN ('reserved','requested','restoring','restored','adopted','deleting','deleted')),
    ADD CHECK ((adopted_database_id IS NULL)=(adopted_at IS NULL)),
    ADD CHECK (adopted_database_id IS NULL OR adopted_database_id=target_owner_id),
    ADD CHECK ((adopted_at IS NULL AND state<>'adopted') OR (adopted_at IS NOT NULL AND state IN ('adopted','deleting','deleted'))),
    ADD CHECK (adopted_at IS NULL OR (restored_at IS NOT NULL AND adopted_at>=restored_at)),
    ADD CHECK (adopted_at IS NULL OR deletion_started_at IS NULL OR deletion_started_at>=adopted_at),
    ADD CONSTRAINT clone_postgres_snapshot_fork_adoption_shape CHECK ((state='reserved' AND request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state='requested' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state='restoring' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state IN ('restored','adopted') AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NOT NULL AND delete_operation_ids='[]'::jsonb)
        OR (state IN ('deleting','deleted') AND (
            (request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
            OR (request_started_at IS NOT NULL AND ((target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                OR (target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL))))));
DROP INDEX project_environment_clone_postgres_fork_account_quota;
CREATE INDEX project_environment_clone_postgres_fork_account_quota
    ON project_environment_clone_postgres_snapshot_restores(account_id)
    WHERE state<>'deleted' AND adopted_database_id IS NULL;

-- +goose Down
-- Transferred ownership cannot be erased by an older protocol.
ALTER TABLE project_environment_clone_postgres_snapshot_restores
    ADD CONSTRAINT fork_adoption_down_no_transfers CHECK (adopted_database_id IS NULL);
DROP INDEX project_environment_clone_postgres_fork_account_quota;
ALTER TABLE project_environment_clone_postgres_snapshot_restores
    DROP CONSTRAINT fork_adoption_down_no_transfers,
    DROP CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check,
    DROP CONSTRAINT clone_postgres_snapshot_fork_adoption_shape,
    DROP COLUMN adopted_database_id,
    DROP COLUMN adopted_at,
    ADD CONSTRAINT project_environment_clone_postgres_snapshot_restore_state_check
        CHECK (state IN ('reserved','requested','restoring','restored','deleting','deleted')),
    ADD CONSTRAINT project_environment_clone_postgres_snapshot_restores_check5 CHECK ((state='reserved' AND request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state='requested' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state='restoring' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
        OR (state='restored' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NOT NULL AND delete_operation_ids='[]'::jsonb)
        OR (state IN ('deleting','deleted') AND (
            (request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
            OR (request_started_at IS NOT NULL AND ((target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL AND delete_operation_ids='[]'::jsonb)
                OR (target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL))))));
CREATE INDEX project_environment_clone_postgres_fork_account_quota
    ON project_environment_clone_postgres_snapshot_restores(account_id) WHERE state<>'deleted';
