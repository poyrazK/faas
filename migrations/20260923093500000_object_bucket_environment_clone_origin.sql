-- filename: 20260923093500000_object_bucket_environment_clone_origin.sql

-- +goose Up
-- +goose StatementBegin
alter table object_buckets
    add column environment_clone_source_bucket_id uuid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table object_buckets
    drop column if exists environment_clone_source_bucket_id;
-- +goose StatementEnd
