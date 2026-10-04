-- +goose Up
-- ADR-531: retain one immutable encrypted bootstrap identity per prepared target.
-- This is private metadata, not SQL dispatch or complete dataset readiness.
CREATE TABLE project_environment_clone_postgres_target_sql_pins (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    target_database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id),
    target_provider_resource_id text NOT NULL CHECK (length(target_provider_resource_id) BETWEEN 1 AND 255),
    target_provider_created_at timestamptz NOT NULL CHECK (isfinite(target_provider_created_at)),
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
    inventory_fingerprint text NOT NULL CHECK (inventory_fingerprint ~ '^[0-9a-f]{64}$'),
    target_fingerprint text NOT NULL CHECK (target_fingerprint ~ '^[0-9a-f]{64}$'),
    key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>0),
    ciphertext_sha256 text NOT NULL CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
    captured_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(captured_at)),
    PRIMARY KEY (operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_inventories(operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_copy_targets(operation_id,source_database_id),
    CHECK (target_database_id<>operation_id AND target_database_id<>source_database_id),
    CHECK (captured_at>=target_provider_created_at)
);

-- +goose Down
-- Erasing committed pins would allow SQL identity substitution on recovery.
ALTER TABLE project_environment_clone_postgres_target_sql_pins
    ADD CONSTRAINT postgres_target_sql_pins_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_target_sql_pins;
