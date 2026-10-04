-- +goose Up
-- ADR-531: the durable clone owns the barrier; a worker lease does not.
ALTER TABLE object_bucket_write_fences
    ADD COLUMN clone_operation_id uuid REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT object_bucket_write_fences_clone_token_check
        CHECK (clone_operation_id IS NULL OR token = clone_operation_id);
CREATE INDEX object_bucket_write_fences_clone_idx
    ON object_bucket_write_fences(clone_operation_id) WHERE clone_operation_id IS NOT NULL;

-- +goose Down
DROP INDEX object_bucket_write_fences_clone_idx;
ALTER TABLE object_bucket_write_fences DROP CONSTRAINT object_bucket_write_fences_clone_token_check,
    DROP COLUMN clone_operation_id;
