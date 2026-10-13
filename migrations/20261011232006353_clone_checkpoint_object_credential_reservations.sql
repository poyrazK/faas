-- filename: 20261011232006353_clone_checkpoint_object_credential_reservations.sql

-- +goose Up
-- ADR-590: immutable fresh credential identities for the complete verified object graph.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_credential_ids (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=16777216),
 plan_sha256 text NOT NULL CHECK(plan_sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'planned' CHECK(state='planned')
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_credential_ids IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_credential_ids) THEN
  RAISE EXCEPTION 'checkpoint credential identities are still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_credential_ids_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_credential_ids;
