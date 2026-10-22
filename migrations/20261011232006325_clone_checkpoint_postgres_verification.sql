-- +goose Up
-- ADR-590: bounded original snapshot/fork data verification authority.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_postgres_verification (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','verified')),
 requested_at timestamptz,
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 verified_at timestamptz,
 PRIMARY KEY(operation_id,source_database_id),
 FOREIGN KEY(operation_id,source_database_id) REFERENCES project_environment_clone_checkpoint_postgres_isolation(operation_id,source_database_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation_sha256 IS NULL AND verified_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation_sha256 IS NULL AND verified_at IS NULL)
 OR (state='verified' AND requested_at IS NOT NULL AND observation_sha256 IS NOT NULL AND verified_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_postgres_verification IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_postgres_verification) THEN
  RAISE EXCEPTION 'checkpoint data verification authority is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_postgres_verification_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_postgres_verification;
