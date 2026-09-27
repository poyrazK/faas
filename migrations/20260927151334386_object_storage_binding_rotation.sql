-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_storage_s3_credentials
    ADD COLUMN rotation_parent_id uuid REFERENCES object_storage_s3_credentials(id) ON DELETE CASCADE,
    ADD COLUMN rotation_wake_id uuid,
    ADD COLUMN rotation_stamped_at timestamptz,
    ADD CONSTRAINT object_storage_s3_credentials_rotation_shape_check CHECK (
        (rotation_parent_id IS NULL AND rotation_wake_id IS NULL AND rotation_stamped_at IS NULL)
        OR (rotation_parent_id IS NOT NULL AND rotation_wake_id IS NOT NULL
            AND managed_app_id IS NULL AND managed_scope IS NULL AND managed_prefix IS NULL)
    );

CREATE UNIQUE INDEX object_storage_s3_credentials_rotation_parent_idx
    ON object_storage_s3_credentials (rotation_parent_id)
    WHERE rotation_parent_id IS NOT NULL AND status = 'active';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS object_storage_s3_credentials_rotation_parent_idx;
ALTER TABLE object_storage_s3_credentials
    DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_rotation_shape_check,
    DROP COLUMN IF EXISTS rotation_stamped_at,
    DROP COLUMN IF EXISTS rotation_wake_id,
    DROP COLUMN IF EXISTS rotation_parent_id;
-- +goose StatementEnd
