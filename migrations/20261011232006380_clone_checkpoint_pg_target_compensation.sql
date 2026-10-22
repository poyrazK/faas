-- +goose Up
-- ADR-590: permanent native request fencing, not deletion/not-found alone,
-- authorizes retirement of the original PostgreSQL checkpoint target.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_target_compensations (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=1048576),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','compensated')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=1048576),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 compensated_at timestamptz,
 PRIMARY KEY(operation_id,source_database_id),
 UNIQUE(operation_id,source_database_id,observation_sha256),
 FOREIGN KEY(operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND compensated_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND compensated_at IS NULL)
 OR (state='compensated' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND compensated_at IS NOT NULL AND compensated_at>=requested_at))
);
ALTER TABLE project_environment_clone_postgres_snapshot_restores ADD COLUMN IF NOT EXISTS checkpoint_compensation_observation_sha256 text;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_snapshot_restores'::regclass AND conname='clone_checkpoint_pg_target_terminal_proof_fk') THEN
  ALTER TABLE project_environment_clone_postgres_snapshot_restores ADD CONSTRAINT clone_checkpoint_pg_target_terminal_proof_fk
 FOREIGN KEY(operation_id,source_database_id,checkpoint_compensation_observation_sha256)
 REFERENCES project_environment_clone_checkpoint_pg_target_compensations(operation_id,source_database_id,observation_sha256) ON DELETE RESTRICT;
 END IF;
END $clone_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_snapshot_restores'::regclass AND conname='clone_checkpoint_pg_target_terminal_proof_shape') THEN
  ALTER TABLE project_environment_clone_postgres_snapshot_restores ADD CONSTRAINT clone_checkpoint_pg_target_terminal_proof_shape
 CHECK(checkpoint_compensation_observation_sha256 IS NULL OR state='deleted');
 END IF;
END $clone_replay$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_snapshot_restores DROP CONSTRAINT IF EXISTS project_environment_clone_postgres_snapshot_restores_check6;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_snapshot_restores'::regclass AND conname='project_environment_clone_postgres_snapshot_restores_check6') THEN
  ALTER TABLE project_environment_clone_postgres_snapshot_restores ADD CONSTRAINT project_environment_clone_postgres_snapshot_restores_check6
 CHECK(state<>'deleted' OR request_started_at IS NULL
 OR (target_provider_resource_id IS NOT NULL AND jsonb_array_length(delete_operation_ids)>0)
 OR checkpoint_compensation_observation_sha256 IS NOT NULL);
 END IF;
END $clone_replay$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_target_compensations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_target_compensations) THEN
  RAISE EXCEPTION 'checkpoint PostgreSQL target compensation authority is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_pg_target_compensations_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_snapshot_restores
 DROP CONSTRAINT project_environment_clone_postgres_snapshot_restores_check6,
 DROP COLUMN checkpoint_compensation_observation_sha256,
 ADD CONSTRAINT project_environment_clone_postgres_snapshot_restores_check6
 CHECK(state<>'deleted' OR request_started_at IS NULL OR (target_provider_resource_id IS NOT NULL AND jsonb_array_length(delete_operation_ids)>0));
DROP TABLE project_environment_clone_checkpoint_pg_target_compensations;
