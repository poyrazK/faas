-- +goose Up
-- ADR-590: original verified fork private runtime readiness.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_postgres_runtime (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','ready')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object'),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 data_resource_id text CHECK(length(data_resource_id) BETWEEN 1 AND 255),
 ready_at timestamptz,
 PRIMARY KEY(operation_id,source_database_id),
 FOREIGN KEY(operation_id,source_database_id) REFERENCES project_environment_clone_checkpoint_postgres_verification(operation_id,source_database_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND data_resource_id IS NULL AND ready_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND data_resource_id IS NULL AND ready_at IS NULL)
 OR (state='ready' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND data_resource_id IS NOT NULL AND ready_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_postgres_runtime IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_postgres_runtime) THEN
  RAISE EXCEPTION 'checkpoint runtime authority is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_postgres_runtime_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_postgres_runtime;
