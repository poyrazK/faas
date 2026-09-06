-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_storage_key_grants DROP CONSTRAINT IF EXISTS object_storage_key_grants_max_bytes_check;
ALTER TABLE object_storage_key_grants ADD CONSTRAINT object_storage_key_grants_max_bytes_check
    CHECK (max_bytes BETWEEN 0 AND 5497558138880);
CREATE TABLE IF NOT EXISTS object_storage_multipart_uploads (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    object_key text NOT NULL CHECK (length(object_key) BETWEEN 1 AND 1024),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 5497558138880),
    part_size_bytes bigint NOT NULL CHECK (part_size_bytes BETWEEN 1 AND 5368709120),
    part_count integer NOT NULL CHECK (part_count BETWEEN 1 AND 10000),
    content_type text NOT NULL DEFAULT '' CHECK (length(content_type) <= 255),
    provider_upload_id text NOT NULL DEFAULT '' CHECK (length(provider_upload_id) <= 4096),
    completion_parts jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(completion_parts) = 'array'),
    state text NOT NULL DEFAULT 'initiating' CHECK (state IN ('initiating','active','completing','aborting','completed','aborted')),
    expires_at timestamptz NOT NULL,
    lease_token text,
    lease_until timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 30),
    retry_at timestamptz NOT NULL DEFAULT now(),
    last_error_code text NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (state = 'initiating' OR provider_upload_id <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS object_storage_multipart_live_key_idx
    ON object_storage_multipart_uploads (bucket_id, object_key)
    WHERE state IN ('initiating','active','completing','aborting');
CREATE INDEX IF NOT EXISTS object_storage_multipart_retry_idx
    ON object_storage_multipart_uploads (retry_at, id)
    WHERE state IN ('initiating','completing','aborting');
CREATE INDEX IF NOT EXISTS object_storage_multipart_expiry_idx
    ON object_storage_multipart_uploads (expires_at, id)
    WHERE state = 'active';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_storage_multipart_uploads;
ALTER TABLE object_storage_key_grants DROP CONSTRAINT IF EXISTS object_storage_key_grants_max_bytes_check;
ALTER TABLE object_storage_key_grants ADD CONSTRAINT object_storage_key_grants_max_bytes_check
    CHECK (max_bytes BETWEEN 0 AND 5368709120);
-- +goose StatementEnd
