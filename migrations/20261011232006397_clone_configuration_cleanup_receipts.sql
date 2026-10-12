-- +goose Up
-- ADR-590: private copied-configuration cleanup, retaining workload ownership.
CREATE TABLE IF NOT EXISTS project_environment_clone_configuration_cleanups (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_configuration_ownership(operation_id) ON DELETE RESTRICT,
 FOREIGN KEY(operation_id) REFERENCES project_environment_clone_checkpoint_workload_retirements(operation_id) ON DELETE RESTRICT,
 ownership_sha256 text NOT NULL CHECK(ownership_sha256 ~ '^[a-f0-9]{64}$'),
 native_plan_sha256 text NOT NULL CHECK(native_plan_sha256 ~ '^[a-f0-9]{64}$'),
 native_observation_sha256 text NOT NULL CHECK(native_observation_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=32768),
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 cleaned_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
LOCK TABLE project_environment_clone_configuration_cleanups IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_configuration_cleanups) THEN
  RAISE EXCEPTION 'configuration cleanup evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_configuration_cleanup_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_configuration_cleanups;
