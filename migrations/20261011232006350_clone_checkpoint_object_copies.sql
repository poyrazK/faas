-- filename: 20261011232006350_clone_checkpoint_object_copies.sql

-- +goose Up
-- ADR-590: bounded once-only retained-object/private-target copy authority.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_copies (
 operation_id uuid NOT NULL,
 source_bucket_id uuid NOT NULL,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=16777216),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','requested','copied')),
 requested_at timestamptz,
 observation_sha256 text CHECK(observation_sha256 ~ '^[a-f0-9]{64}$'),
 copied_at timestamptz,
 PRIMARY KEY(operation_id,source_bucket_id),
 FOREIGN KEY(operation_id,source_bucket_id) REFERENCES project_environment_clone_checkpoint_object_targets(operation_id,source_bucket_id) ON DELETE RESTRICT,
 CHECK((state='planned' AND requested_at IS NULL AND observation_sha256 IS NULL AND copied_at IS NULL)
 OR (state='requested' AND requested_at IS NOT NULL AND observation_sha256 IS NULL AND copied_at IS NULL)
 OR (state='copied' AND requested_at IS NOT NULL AND observation_sha256 IS NOT NULL AND copied_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_copies IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_copies) THEN
  RAISE EXCEPTION 'checkpoint data copy authority is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_copies_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_copies;
