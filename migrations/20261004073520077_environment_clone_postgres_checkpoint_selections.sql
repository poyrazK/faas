-- filename: 20261004073520077_environment_clone_postgres_checkpoint_selections.sql

-- +goose Up
-- ADR-375: immutable pre-checkpoint selection under original source recovery
-- authority. Names and fingerprint keys live only in the private age envelope.
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_checkpoint_selections (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 maintenance_id uuid NOT NULL REFERENCES managed_postgres_checkpoint_maintenance(id) ON DELETE RESTRICT,
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
 key_id text NOT NULL CHECK (key_id<>'' AND length(key_id)<=255),
 ciphertext_sha256 text NOT NULL CHECK (ciphertext_sha256 ~ '^[a-f0-9]{64}$'),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>0 AND octet_length(ciphertext)<=4276224),
 retained_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (operation_id,source_database_id),
 FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_write_fences(operation_id,source_database_id) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE IF EXISTS project_environment_clone_postgres_checkpoint_selections;
