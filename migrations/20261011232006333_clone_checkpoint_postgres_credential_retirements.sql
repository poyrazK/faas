-- +goose Up
-- ADR-590: permanent native preparation fences before fork compensation.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_credential_retirements (
 operation_id uuid NOT NULL,
 source_binding_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','retired')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object'),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 retired_at timestamptz,
 PRIMARY KEY(operation_id,source_binding_id),
 FOREIGN KEY(operation_id,source_binding_id) REFERENCES project_environment_clone_checkpoint_postgres_credentials(operation_id,source_binding_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='retired' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND retired_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_credential_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_credential_retirements) THEN
  RAISE EXCEPTION 'checkpoint credential retirement evidence is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_postgres_credential_retirements_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_pg_credential_retirements;
