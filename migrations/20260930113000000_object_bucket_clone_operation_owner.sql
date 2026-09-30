-- ADR-375: a full-copy target belongs to exactly one durable clone operation.
-- +goose Up
ALTER TABLE object_buckets ADD COLUMN environment_clone_operation_id uuid;
ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_clone_operation_origin_check
    CHECK (environment_clone_operation_id IS NULL OR environment_clone_source_bucket_id IS NOT NULL);

-- +goose Down
ALTER TABLE object_buckets DROP CONSTRAINT object_buckets_clone_operation_origin_check;
ALTER TABLE object_buckets DROP COLUMN environment_clone_operation_id;
