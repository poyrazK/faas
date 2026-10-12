-- filename: 20261011232006377_clone_checkpoint_source_compensation.sql

-- +goose Up
-- ADR-590: once-only original source cleanup authority survives source reopening.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_source_compensations (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=16777216),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','compensated')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=16777216),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 compensated_at timestamptz,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND compensated_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND compensated_at IS NULL)
 OR (state='compensated' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND compensated_at IS NOT NULL AND compensated_at>=requested_at))
);

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_source_compensations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_source_compensations) THEN
  RAISE EXCEPTION 'checkpoint source compensation authority is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_source_compensations_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_source_compensations;
