-- filename: 20261011232006356_clone_checkpoint_object_credential_values.sql

-- +goose Up
-- ADR-590: private sealed gateway credentials and runtime values, without publication.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_values (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_checkpoint_object_credential_ids(operation_id) ON DELETE RESTRICT,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16777216),
 payload_sha256 text NOT NULL CHECK(payload_sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'sealed' CHECK(state='sealed')
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_values IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_values) THEN
  RAISE EXCEPTION 'sealed checkpoint object values are still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_values_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_values;
