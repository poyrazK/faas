-- +goose Up
-- ADR-531: reserve private archive ownership before an uncertain storage write.
-- An archive receipt grants no cluster-global import or dataset readiness.
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_archives (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    owner_id uuid NOT NULL UNIQUE,
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
    inventory_fingerprint text NOT NULL CHECK (inventory_fingerprint ~ '^[0-9a-f]{64}$'),
    key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
    storage_id text NOT NULL CHECK (length(storage_id) BETWEEN 1 AND 255),
    storage_fingerprint text NOT NULL CHECK (storage_fingerprint ~ '^[0-9a-f]{64}$'),
    storage_key text NOT NULL UNIQUE,
    reserved_bytes bigint NOT NULL CHECK (reserved_bytes>0),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','uploading','retained')),
    upload_started_at timestamptz CHECK (isfinite(upload_started_at)),
    plaintext_bytes bigint CHECK (plaintext_bytes>=5),
    ciphertext_bytes bigint CHECK (ciphertext_bytes>0 AND ciphertext_bytes<=reserved_bytes),
    ciphertext_sha256 text CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
    retained_at timestamptz CHECK (isfinite(retained_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY (operation_id,source_database_id,database_oid),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_inventories(operation_id,source_database_id),
    CHECK (owner_id<>operation_id AND owner_id<>source_database_id),
    CHECK (storage_key='postgres-copies/' || operation_id::text || '/' || owner_id::text || '.age'),
    CHECK (upload_started_at IS NULL OR upload_started_at>=created_at),
    CHECK (retained_at IS NULL OR retained_at>=upload_started_at),
    CHECK ((state='reserved' AND upload_started_at IS NULL AND retained_at IS NULL AND plaintext_bytes IS NULL AND ciphertext_bytes IS NULL AND ciphertext_sha256 IS NULL)
        OR (state='uploading' AND upload_started_at IS NOT NULL AND retained_at IS NULL AND plaintext_bytes IS NULL AND ciphertext_bytes IS NULL AND ciphertext_sha256 IS NULL)
        OR (state='retained' AND upload_started_at IS NOT NULL AND retained_at IS NOT NULL AND plaintext_bytes IS NOT NULL AND ciphertext_bytes IS NOT NULL AND ciphertext_sha256 IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS project_environment_clone_postgres_archives_account_holds
    ON project_environment_clone_postgres_archives(account_id);

-- +goose Down
-- Even an undispatched reservation or uncertain write retains ownership.
ALTER TABLE project_environment_clone_postgres_archives
    ADD CONSTRAINT postgres_archives_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_archives;
