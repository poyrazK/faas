-- filename: 20261011232006385_clone_checkpoint_capture_catalogue_retirements.sql

-- +goose Up
-- ADR-590: retained catalogue retirement keeps original ownership evidence.
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_checkpoint_capture_retirements'::regclass AND conname='clone_checkpoint_capture_retirement_proof_key') THEN
  ALTER TABLE project_environment_clone_checkpoint_capture_retirements ADD CONSTRAINT clone_checkpoint_capture_retirement_proof_key UNIQUE(operation_id,observation_sha256);
 END IF;
END $clone_replay$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_capture_catalogues (
 operation_id uuid NOT NULL,
 source_kind text NOT NULL CHECK(source_kind IN ('managed_postgres','object_storage')),
 source_id uuid NOT NULL,
 capture_identity text NOT NULL CHECK(capture_identity<>''),
 source_resource_id text NOT NULL CHECK(source_resource_id<>''),
 observation_sha256 text NOT NULL CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 retired_at timestamptz NOT NULL,
 PRIMARY KEY(operation_id,source_kind,source_id),
 FOREIGN KEY(operation_id,observation_sha256) REFERENCES project_environment_clone_checkpoint_capture_retirements(operation_id,observation_sha256) ON DELETE RESTRICT
);

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_capture_retirements, project_environment_clone_checkpoint_capture_catalogues IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_capture_retirements)
 OR EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_capture_catalogues) THEN
  RAISE EXCEPTION 'capture retirement authority or catalogue proof is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_capture_catalogue_retirements_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_capture_catalogues;
ALTER TABLE project_environment_clone_checkpoint_capture_retirements DROP CONSTRAINT clone_checkpoint_capture_retirement_proof_key;
