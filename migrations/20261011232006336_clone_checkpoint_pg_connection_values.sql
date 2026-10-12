-- +goose Up
-- ADR-590: immutable private sealed connection values.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_connection_values (
 operation_id uuid NOT NULL,
 source_binding_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','sealed')),
 ciphertext bytea CHECK(octet_length(ciphertext) BETWEEN 1 AND 4096),
 key_id text CHECK(length(key_id) BETWEEN 1 AND 255),
 ciphertext_sha256 text CHECK(ciphertext_sha256 ~ '^[a-f0-9]{64}$'),
 sealed_at timestamptz,
 PRIMARY KEY(operation_id,source_binding_id),
 FOREIGN KEY(operation_id,source_binding_id) REFERENCES project_environment_clone_checkpoint_postgres_credentials(operation_id,source_binding_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND ciphertext IS NULL AND key_id IS NULL AND ciphertext_sha256 IS NULL AND sealed_at IS NULL)
 OR (state='sealed' AND ciphertext IS NOT NULL AND key_id IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND sealed_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_connection_values IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_connection_values) THEN
  RAISE EXCEPTION 'checkpoint connection values are still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_pg_connection_values_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_pg_connection_values;
