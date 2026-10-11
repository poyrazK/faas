-- +goose Up
-- ADR-531: the durable clone owns the barrier; a worker lease does not.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='object_bucket_write_fences'::regclass AND attname='clone_operation_id' AND NOT attisdropped) THEN
        ALTER TABLE object_bucket_write_fences
            ADD COLUMN IF NOT EXISTS clone_operation_id uuid REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
            ADD CONSTRAINT object_bucket_write_fences_clone_token_check
                CHECK (clone_operation_id IS NULL OR token = clone_operation_id);
    END IF;
END;
$$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS object_bucket_write_fences_clone_idx
    ON object_bucket_write_fences(clone_operation_id) WHERE clone_operation_id IS NOT NULL;

-- +goose Down
DROP INDEX object_bucket_write_fences_clone_idx;
ALTER TABLE object_bucket_write_fences DROP CONSTRAINT object_bucket_write_fences_clone_token_check,
    DROP COLUMN clone_operation_id;
