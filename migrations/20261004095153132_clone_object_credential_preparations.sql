-- ADR-531: fresh sealed credentials and envelopes have durable retry identities.
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_object_credentials (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    source_credential_id uuid NOT NULL,
    target_credential_id uuid NOT NULL REFERENCES object_storage_s3_credentials(id) ON DELETE CASCADE,
    preparation_hash text NOT NULL CHECK (preparation_hash ~ '^[a-f0-9]{64}$'),
    preparation jsonb NOT NULL CHECK (jsonb_typeof(preparation) = 'object'),
    PRIMARY KEY (operation_id, source_credential_id),
    UNIQUE (target_credential_id),
    CHECK (source_credential_id <> target_credential_id)
);

-- +goose Down
DROP TABLE project_environment_clone_object_credentials;
