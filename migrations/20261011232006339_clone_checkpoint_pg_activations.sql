-- +goose Up
-- ADR-590: one immutable native activation manifest per checkpoint operation.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_activations (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=16777216),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','active')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object'),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 activated_at timestamptz,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND activated_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND activated_at IS NULL)
 OR (state='active' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND activated_at IS NOT NULL))
);
ALTER TABLE project_environment_clone_checkpoint_pg_connection_values ADD COLUMN IF NOT EXISTS activation_operation_id uuid REFERENCES project_environment_clone_checkpoint_pg_activations(operation_id) ON DELETE RESTRICT;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_checkpoint_pg_connection_values'::regclass AND conname='clone_checkpoint_pg_connection_activation_owner') THEN
  ALTER TABLE project_environment_clone_checkpoint_pg_connection_values ADD CONSTRAINT clone_checkpoint_pg_connection_activation_owner CHECK(activation_operation_id IS NULL OR activation_operation_id=operation_id);
 END IF;
END $clone_replay$;
-- +goose StatementEnd
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_activations,project_environment_clone_checkpoint_pg_connection_values IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_activations) OR EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_connection_values WHERE activation_operation_id IS NOT NULL) THEN
  RAISE EXCEPTION 'checkpoint activation authority is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_pg_activations_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_checkpoint_pg_connection_values DROP COLUMN activation_operation_id;
DROP TABLE project_environment_clone_checkpoint_pg_activations;
