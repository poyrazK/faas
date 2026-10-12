-- +goose Up
-- ADR-590: atomic removal of unborrowed original workload catalogue rows.
CREATE TABLE IF NOT EXISTS project_environment_clone_workload_cleanups (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_configuration_cleanups(operation_id) ON DELETE RESTRICT,
 configuration_cleanup_sha256 text NOT NULL CHECK(configuration_cleanup_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=32768),
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 cleaned_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
LOCK TABLE project_environment_clone_workload_cleanups IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_workload_cleanups) THEN
  RAISE EXCEPTION 'workload cleanup evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_workload_cleanup_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_workload_cleanups;
