-- +goose Up
-- ADR-531: original encrypted complete database creation plan before target CREATE DATABASE.
-- Exact prerequisite ciphertexts are ownership, not database/readiness evidence.
CREATE TABLE project_environment_clone_postgres_database_plans (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 target_database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id),
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 inventory_fingerprint text NOT NULL CHECK (inventory_fingerprint ~ '^[0-9a-f]{64}$'),
 inventory_ciphertext_sha256 text NOT NULL CHECK (inventory_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 target_fingerprint text NOT NULL CHECK (target_fingerprint ~ '^[0-9a-f]{64}$'),
 target_pins_ciphertext_sha256 text NOT NULL CHECK (target_pins_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 role_plan_ciphertext_sha256 text NOT NULL CHECK (role_plan_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>0),
 ciphertext_sha256 text NOT NULL CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(captured_at)),
 PRIMARY KEY (operation_id,source_database_id),
 FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_role_plans(operation_id,source_database_id),
 CHECK (target_database_id<>operation_id AND target_database_id<>source_database_id)
);

-- +goose Down
ALTER TABLE project_environment_clone_postgres_database_plans
 ADD CONSTRAINT postgres_database_plans_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_database_plans;
