-- A primary workload may wait for named long-running companions at startup.
-- The immutable request contract is stored with the deployment snapshot.
--
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS override_main_depends_on jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE deployments
  ADD CONSTRAINT deployments_main_depends_on_shape_chk
  CHECK (jsonb_typeof(override_main_depends_on) = 'array'
         AND jsonb_array_length(override_main_depends_on) <= 6);

-- +goose Down
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_main_depends_on_shape_chk;

ALTER TABLE deployments
  DROP COLUMN IF EXISTS override_main_depends_on;
