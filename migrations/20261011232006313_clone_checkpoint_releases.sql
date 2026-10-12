-- +goose Up
-- ADR-590: original successful release authority, independent of mutable source configuration.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_releases (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan)='object'),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 adoption_sha256 text NOT NULL CHECK (adoption_sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'planned' CHECK (state IN ('planned','requested','released')),
 requested_at timestamptz,
 observation_sha256 text CHECK (observation_sha256 ~ '^[a-f0-9]{64}$'),
 released_at timestamptz,
 CHECK ((state='planned' AND requested_at IS NULL AND observation_sha256 IS NULL AND released_at IS NULL)
  OR (state='requested' AND requested_at IS NOT NULL AND observation_sha256 IS NULL AND released_at IS NULL)
  OR (state='released' AND requested_at IS NOT NULL AND observation_sha256 IS NOT NULL AND released_at IS NOT NULL))
);

-- +goose Down
-- Never discard dispatch/recovery authority while any original release exists.
LOCK TABLE project_environment_clone_checkpoint_releases IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM project_environment_clone_checkpoint_releases) THEN
  RAISE EXCEPTION 'checkpoint release recovery authority is still owned'
   USING ERRCODE='23514', CONSTRAINT='clone_checkpoint_releases_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_releases;
