-- ADR-531: a full-copy target belongs to exactly one durable clone operation.
-- +goose Up
ALTER TABLE object_buckets ADD COLUMN IF NOT EXISTS environment_clone_operation_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_buckets'::regclass AND conname='object_buckets_clone_operation_origin_check') THEN
        ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_clone_operation_origin_check
            CHECK (environment_clone_operation_id IS NULL OR environment_clone_source_bucket_id IS NOT NULL);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE object_buckets DROP CONSTRAINT object_buckets_clone_operation_origin_check;
ALTER TABLE object_buckets DROP COLUMN environment_clone_operation_id;
