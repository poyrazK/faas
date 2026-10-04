-- +goose Up
-- ADR-531: encrypted metadata for the exact retained snapshot/native capture.
-- This row never supplies data export/import, writer release or readiness proof.
CREATE TABLE project_environment_clone_postgres_inventories (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    capture_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id),
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>0),
    ciphertext_sha256 text NOT NULL CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
    captured_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(captured_at)),
    PRIMARY KEY (operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id),
    CHECK (capture_database_id<>source_database_id)
);

-- +goose Down
-- A committed capture is durable input even after provider resource cleanup.
ALTER TABLE project_environment_clone_postgres_inventories
    ADD CONSTRAINT postgres_inventories_down_no_capture CHECK (false);
DROP TABLE project_environment_clone_postgres_inventories;
