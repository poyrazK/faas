-- +goose Up
-- ADR-590: sealed pre-dispatch inactive stage credential ownership.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_postgres_credentials (
 operation_id uuid NOT NULL,
 source_binding_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 target_binding_id uuid NOT NULL UNIQUE,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','sealed','requested','prepared')),
 ciphertext bytea CHECK(octet_length(ciphertext) BETWEEN 1 AND 4096),
 key_id text CHECK(length(key_id) BETWEEN 1 AND 255),
 ciphertext_sha256 text CHECK(ciphertext_sha256 ~ '^[a-f0-9]{64}$'),
 requested_at timestamptz,
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 prepared_at timestamptz,
 PRIMARY KEY(operation_id,source_binding_id),
 FOREIGN KEY(operation_id,source_database_id) REFERENCES project_environment_clone_checkpoint_postgres_runtime(operation_id,source_database_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND ciphertext IS NULL AND key_id IS NULL AND ciphertext_sha256 IS NULL AND requested_at IS NULL AND observation_sha256 IS NULL AND prepared_at IS NULL)
 OR (state='sealed' AND ciphertext IS NOT NULL AND key_id IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND requested_at IS NULL AND observation_sha256 IS NULL AND prepared_at IS NULL)
 OR (state='requested' AND ciphertext IS NOT NULL AND key_id IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND requested_at IS NOT NULL AND observation_sha256 IS NULL AND prepared_at IS NULL)
 OR (state='prepared' AND ciphertext IS NOT NULL AND key_id IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND requested_at IS NOT NULL AND observation_sha256 IS NOT NULL AND prepared_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_postgres_credentials IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_postgres_credentials) THEN
  RAISE EXCEPTION 'checkpoint credential authority is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_postgres_credentials_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_postgres_credentials;
