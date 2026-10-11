-- +goose Up
-- ADR-531: each encrypted child identity retains the original creation receipt.
-- The archive FK bounds pins to already charged source-database reservations.
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_database_sql_pins (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 target_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id),
 archive_owner_id uuid NOT NULL REFERENCES project_environment_clone_postgres_archives(owner_id),
 archive_reservation_sha256 text NOT NULL CHECK (archive_reservation_sha256 ~ '^[0-9a-f]{64}$'),
 database_plan_ciphertext_sha256 text NOT NULL CHECK (database_plan_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 target_provider_resource_id text NOT NULL CHECK (length(target_provider_resource_id) BETWEEN 1 AND 255),
 target_provider_created_at timestamptz NOT NULL CHECK (isfinite(target_provider_created_at)),
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 inventory_fingerprint text NOT NULL CHECK (inventory_fingerprint ~ '^[0-9a-f]{64}$'),
 target_fingerprint text NOT NULL CHECK (target_fingerprint ~ '^[0-9a-f]{64}$'),
 key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>0),
 ciphertext_sha256 text NOT NULL CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(captured_at)),
 PRIMARY KEY (operation_id,source_database_id,database_oid),
 UNIQUE (operation_id,source_database_id,target_fingerprint),
 FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_database_plans(operation_id,source_database_id),
 FOREIGN KEY (operation_id,source_database_id,database_oid) REFERENCES project_environment_clone_postgres_archives(operation_id,source_database_id,database_oid),
 CHECK (target_database_id<>operation_id AND target_database_id<>source_database_id),
 CHECK (captured_at>=target_provider_created_at)
);

-- +goose Down
ALTER TABLE project_environment_clone_postgres_database_sql_pins
 ADD CONSTRAINT postgres_database_sql_pins_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_database_sql_pins;
