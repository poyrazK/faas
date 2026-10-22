-- +goose Up
-- ADR-590: native owner-wide fencing closes unknown create/cleanup/writer outcomes.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_copy_retirements (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=2097152),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','retired')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=2097152),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 retired_at timestamptz,
 PRIMARY KEY(operation_id,source_database_id),
 UNIQUE(operation_id,source_database_id,observation_sha256),
 FOREIGN KEY(operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='retired' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND retired_at IS NOT NULL AND retired_at>=requested_at))
);
ALTER TABLE project_environment_clone_postgres_copy_readers ADD COLUMN IF NOT EXISTS checkpoint_retirement_observation_sha256 text;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_readers'::regclass AND conname='clone_checkpoint_pg_copy_readers_proof_fk') THEN
  ALTER TABLE project_environment_clone_postgres_copy_readers ADD CONSTRAINT clone_checkpoint_pg_copy_readers_proof_fk
 FOREIGN KEY(operation_id,source_database_id,checkpoint_retirement_observation_sha256)
 REFERENCES project_environment_clone_checkpoint_pg_copy_retirements(operation_id,source_database_id,observation_sha256) ON DELETE RESTRICT;
 END IF;
END $clone_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_readers'::regclass AND conname='clone_checkpoint_pg_copy_readers_proof_shape') THEN
  ALTER TABLE project_environment_clone_postgres_copy_readers ADD CONSTRAINT clone_checkpoint_pg_copy_readers_proof_shape
 CHECK(checkpoint_retirement_observation_sha256 IS NULL OR state='retired');
 END IF;
END $clone_replay$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_copy_readers DROP CONSTRAINT IF EXISTS project_environment_clone_postgres_copy_readers_check8;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_readers'::regclass AND conname='project_environment_clone_postgres_copy_readers_check8') THEN
  ALTER TABLE project_environment_clone_postgres_copy_readers ADD CONSTRAINT project_environment_clone_postgres_copy_readers_check8 CHECK(((((state = 'reserved'::text) AND (request_started_at IS NULL) AND (endpoint_id IS NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'requested'::text) AND (request_started_at IS NOT NULL) AND (endpoint_id IS NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'observed'::text) AND (request_started_at IS NOT NULL) AND (endpoint_id IS NOT NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'deleting'::text) AND (cleanup_requested_at IS NOT NULL) AND (retired_at IS NULL)) OR ((state = 'retired'::text) AND (cleanup_requested_at IS NOT NULL) AND (retired_at IS NOT NULL) AND (((request_started_at IS NULL) AND (endpoint_id IS NULL) AND (cleanup_dispatched_at IS NULL) AND (delete_operation_ids = '[]'::jsonb) AND (capture_operation_ids = '[]'::jsonb)) OR ((request_started_at IS NOT NULL) AND (endpoint_id IS NOT NULL) AND ((delete_operation_ids <> '[]'::jsonb) OR (capture_operation_ids <> '[]'::jsonb))))))) OR (state='retired' AND retired_at IS NOT NULL AND checkpoint_retirement_observation_sha256 IS NOT NULL AND cleanup_requested_at IS NOT NULL));
 END IF;
END $clone_replay$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_copy_targets ADD COLUMN IF NOT EXISTS checkpoint_retirement_observation_sha256 text;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_targets'::regclass AND conname='clone_checkpoint_pg_copy_targets_proof_fk') THEN
  ALTER TABLE project_environment_clone_postgres_copy_targets ADD CONSTRAINT clone_checkpoint_pg_copy_targets_proof_fk
 FOREIGN KEY(operation_id,source_database_id,checkpoint_retirement_observation_sha256)
 REFERENCES project_environment_clone_checkpoint_pg_copy_retirements(operation_id,source_database_id,observation_sha256) ON DELETE RESTRICT;
 END IF;
END $clone_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_targets'::regclass AND conname='clone_checkpoint_pg_copy_targets_proof_shape') THEN
  ALTER TABLE project_environment_clone_postgres_copy_targets ADD CONSTRAINT clone_checkpoint_pg_copy_targets_proof_shape
 CHECK(checkpoint_retirement_observation_sha256 IS NULL OR state='retired');
 END IF;
END $clone_replay$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_copy_targets DROP CONSTRAINT IF EXISTS clone_copy_target_lifecycle_shape;
-- +goose StatementBegin
DO $clone_replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_targets'::regclass AND conname='clone_copy_target_lifecycle_shape') THEN
  ALTER TABLE project_environment_clone_postgres_copy_targets ADD CONSTRAINT clone_copy_target_lifecycle_shape CHECK(((((state = 'reserved'::text) AND (request_started_at IS NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'requested'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'preparing'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'prepared'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (prepared_at IS NOT NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'deleting'::text) AND (request_started_at IS NOT NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NOT NULL) AND (deletion_observed_at IS NULL) AND (((provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL)) OR ((provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL)))) OR ((state = 'retired'::text) AND (retired_at IS NOT NULL) AND (((request_started_at IS NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (deletion_started_at IS NOT NULL) AND (deletion_observed_at IS NOT NULL)))))) OR (state='retired' AND retired_at IS NOT NULL AND checkpoint_retirement_observation_sha256 IS NOT NULL AND ((provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL) OR (provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND request_started_at IS NOT NULL))));
 END IF;
END $clone_replay$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_copy_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_copy_retirements) THEN
  RAISE EXCEPTION 'checkpoint PostgreSQL copy retirement authority is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_pg_copy_retirements_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_postgres_copy_readers
 DROP CONSTRAINT project_environment_clone_postgres_copy_readers_check8,
 DROP COLUMN checkpoint_retirement_observation_sha256,
 ADD CONSTRAINT project_environment_clone_postgres_copy_readers_check8 CHECK ((((state = 'reserved'::text) AND (request_started_at IS NULL) AND (endpoint_id IS NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'requested'::text) AND (request_started_at IS NOT NULL) AND (endpoint_id IS NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'observed'::text) AND (request_started_at IS NOT NULL) AND (endpoint_id IS NOT NULL) AND (cleanup_requested_at IS NULL) AND (retired_at IS NULL)) OR ((state = 'deleting'::text) AND (cleanup_requested_at IS NOT NULL) AND (retired_at IS NULL)) OR ((state = 'retired'::text) AND (cleanup_requested_at IS NOT NULL) AND (retired_at IS NOT NULL) AND (((request_started_at IS NULL) AND (endpoint_id IS NULL) AND (cleanup_dispatched_at IS NULL) AND (delete_operation_ids = '[]'::jsonb) AND (capture_operation_ids = '[]'::jsonb)) OR ((request_started_at IS NOT NULL) AND (endpoint_id IS NOT NULL) AND ((delete_operation_ids <> '[]'::jsonb) OR (capture_operation_ids <> '[]'::jsonb)))))));
ALTER TABLE project_environment_clone_postgres_copy_targets
 DROP CONSTRAINT clone_copy_target_lifecycle_shape,
 DROP COLUMN checkpoint_retirement_observation_sha256,
 ADD CONSTRAINT clone_copy_target_lifecycle_shape CHECK ((((state = 'reserved'::text) AND (request_started_at IS NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'requested'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'preparing'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (prepared_at IS NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'prepared'::text) AND (request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (prepared_at IS NOT NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((state = 'deleting'::text) AND (request_started_at IS NOT NULL) AND (retired_at IS NULL) AND (deletion_started_at IS NOT NULL) AND (deletion_observed_at IS NULL) AND (((provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL)) OR ((provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL)))) OR ((state = 'retired'::text) AND (retired_at IS NOT NULL) AND (((request_started_at IS NULL) AND (provider_resource_id IS NULL) AND (provider_created_at IS NULL) AND (observed_at IS NULL) AND (prepared_at IS NULL) AND (deletion_started_at IS NULL) AND (deletion_observed_at IS NULL)) OR ((request_started_at IS NOT NULL) AND (provider_resource_id IS NOT NULL) AND (provider_created_at IS NOT NULL) AND (observed_at IS NOT NULL) AND (deletion_started_at IS NOT NULL) AND (deletion_observed_at IS NOT NULL))))));
DROP TABLE project_environment_clone_checkpoint_pg_copy_retirements;
