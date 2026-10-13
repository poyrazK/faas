-- filename: 20261011232006394_clone_configuration_ownership_receipts.sql

-- +goose Up
-- ADR-590: record original configuration ownership at materialization, never later.
CREATE TABLE IF NOT EXISTS project_environment_clone_configuration_ownership (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_materializations(operation_id) ON DELETE RESTRICT,
 environment_id uuid NOT NULL CHECK(environment_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=32768),
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
LOCK TABLE project_environment_clone_configuration_ownership IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_configuration_ownership) THEN
  RAISE EXCEPTION 'original configuration ownership evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_configuration_ownership_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_configuration_ownership;
