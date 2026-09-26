-- A primary workload may wait for named long-running companions at startup.
-- The immutable request contract is stored with the deployment snapshot.
--
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS override_main_depends_on jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'deployments_main_depends_on_shape_chk'
      AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments
      ADD CONSTRAINT deployments_main_depends_on_shape_chk
      CHECK (jsonb_typeof(override_main_depends_on) = 'array'
             AND jsonb_array_length(override_main_depends_on) <= 6);
  END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_main_depends_on_shape_chk;

ALTER TABLE deployments
  DROP COLUMN IF EXISTS override_main_depends_on;
