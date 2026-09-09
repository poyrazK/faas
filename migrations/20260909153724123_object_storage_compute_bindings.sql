-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_storage_s3_credentials
    ADD COLUMN IF NOT EXISTS managed_app_id uuid,
    ADD COLUMN IF NOT EXISTS managed_scope text,
    ADD COLUMN IF NOT EXISTS managed_prefix text;

ALTER TABLE object_storage_s3_credentials
    DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_managed_shape_check;
ALTER TABLE object_storage_s3_credentials
    ADD CONSTRAINT object_storage_s3_credentials_managed_shape_check CHECK (
        (managed_app_id IS NULL AND managed_scope IS NULL AND managed_prefix IS NULL)
        OR (managed_app_id IS NOT NULL
            AND managed_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
            AND managed_prefix ~ '^[A-Z][A-Z0-9_]{0,47}$')
    );

CREATE UNIQUE INDEX IF NOT EXISTS object_storage_s3_credentials_managed_binding_idx
    ON object_storage_s3_credentials (bucket_id, managed_app_id, managed_scope, managed_prefix)
    WHERE status = 'active' AND managed_app_id IS NOT NULL;

ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS managed_object_storage_credential_id uuid;
CREATE INDEX IF NOT EXISTS app_secrets_managed_object_storage_idx
    ON app_secrets (managed_object_storage_credential_id)
    WHERE managed_object_storage_credential_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS app_secrets_managed_object_storage_idx;
ALTER TABLE app_secrets DROP COLUMN IF EXISTS managed_object_storage_credential_id;
DROP INDEX IF EXISTS object_storage_s3_credentials_managed_binding_idx;
ALTER TABLE object_storage_s3_credentials
    DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_managed_shape_check;
ALTER TABLE object_storage_s3_credentials
    DROP COLUMN IF EXISTS managed_app_id,
    DROP COLUMN IF EXISTS managed_scope,
    DROP COLUMN IF EXISTS managed_prefix;
-- +goose StatementEnd
