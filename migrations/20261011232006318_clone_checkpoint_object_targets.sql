-- +goose Up
-- ADR-590: preserve once-only native creation authority for original private targets.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_targets (
 operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 source_bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE RESTRICT,
 target_bucket_id uuid NOT NULL UNIQUE REFERENCES object_buckets(id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan)='object'),
 state text NOT NULL DEFAULT 'planned' CHECK (state IN ('planned','requested','ready')),
 requested_at timestamptz,
 observation_sha256 text CHECK (observation_sha256 ~ '^[a-f0-9]{64}$'),
 ready_at timestamptz,
 PRIMARY KEY(operation_id,source_bucket_id),
 CHECK (source_bucket_id<>target_bucket_id),
 CHECK ((state='planned' AND requested_at IS NULL AND observation_sha256 IS NULL AND ready_at IS NULL)
  OR (state='requested' AND requested_at IS NOT NULL AND observation_sha256 IS NULL AND ready_at IS NULL)
  OR (state='ready' AND requested_at IS NOT NULL AND observation_sha256 IS NOT NULL AND ready_at IS NOT NULL))
);

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_targets IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM project_environment_clone_checkpoint_object_targets) THEN
  RAISE EXCEPTION 'checkpoint target creation recovery authority is still owned'
   USING ERRCODE='23514', CONSTRAINT='clone_checkpoint_object_targets_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_targets;
