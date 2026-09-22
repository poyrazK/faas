-- filename: 20260922134528743_object_storage_multipart_metadata.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_storage_multipart_uploads
    ADD COLUMN IF NOT EXISTS object_metadata jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(object_metadata) = 'object')
    CHECK (octet_length(object_metadata::text) <= 32768);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE object_storage_multipart_uploads DROP COLUMN IF EXISTS object_metadata;
-- +goose StatementEnd
