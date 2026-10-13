-- filename: 20261011232006368_clone_checkpoint_object_retirements.sql

-- +goose Up
-- ADR-590: preserve once-only permanent native cleanup authority and evidence.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_retirements (
 operation_id uuid NOT NULL,
 source_bucket_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=17825792),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','retired')),
 requested_at timestamptz,
 observation jsonb CHECK(jsonb_typeof(observation)='object'),
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 retired_at timestamptz,
 PRIMARY KEY(operation_id,source_bucket_id),
 FOREIGN KEY(operation_id,source_bucket_id) REFERENCES project_environment_clone_checkpoint_object_targets(operation_id,source_bucket_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation IS NULL AND observation_sha256 IS NULL AND retired_at IS NULL)
 OR (state='retired' AND requested_at IS NOT NULL AND observation IS NOT NULL AND observation_sha256 IS NOT NULL AND retired_at IS NOT NULL AND retired_at>=requested_at))
);

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_retirements) THEN
  RAISE EXCEPTION 'checkpoint native object retirement authority is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_retirements_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_retirements;
