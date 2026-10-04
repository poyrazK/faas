-- +goose Up
-- ADR-531: reserve one private SQL import owner per retained database archive.
-- Executed is a command receipt; it grants no dataset/stage readiness.
CREATE TABLE project_environment_clone_postgres_imports (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    import_id uuid NOT NULL UNIQUE,
    archive_owner_id uuid NOT NULL REFERENCES project_environment_clone_postgres_archives(owner_id),
    archive_ciphertext_sha256 text NOT NULL CHECK (archive_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
    target_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id),
    target_provider_resource_id text NOT NULL CHECK (length(target_provider_resource_id) BETWEEN 1 AND 255),
    target_provider_created_at timestamptz NOT NULL CHECK (isfinite(target_provider_created_at)),
    target_fingerprint text NOT NULL CHECK (target_fingerprint ~ '^[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','importing','executed')),
    import_started_at timestamptz CHECK (isfinite(import_started_at)),
    executed_at timestamptz CHECK (isfinite(executed_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY (operation_id,source_database_id,database_oid),
    FOREIGN KEY (operation_id,source_database_id,database_oid) REFERENCES project_environment_clone_postgres_archives(operation_id,source_database_id,database_oid),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_copy_targets(operation_id,source_database_id),
    CHECK (import_id<>operation_id AND import_id<>source_database_id AND import_id<>archive_owner_id AND import_id<>target_database_id),
    CHECK (target_database_id<>source_database_id),
    CHECK (created_at>=target_provider_created_at),
    CHECK (import_started_at IS NULL OR import_started_at>=created_at),
    CHECK (executed_at IS NULL OR executed_at>=import_started_at),
    CHECK ((state='reserved' AND import_started_at IS NULL AND executed_at IS NULL)
        OR (state='importing' AND import_started_at IS NOT NULL AND executed_at IS NULL)
        OR (state='executed' AND import_started_at IS NOT NULL AND executed_at IS NOT NULL))
);

-- +goose Down
-- Uncertain writes and undispatched reservations retain ownership.
ALTER TABLE project_environment_clone_postgres_imports
    ADD CONSTRAINT postgres_imports_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_imports;
