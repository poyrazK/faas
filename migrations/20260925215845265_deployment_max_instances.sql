-- A deployment can opt into a tighter serving-instance ceiling than its app.
-- Zero means inherit the app's effective ceiling; the app/plan cap remains
-- independently enforced across all revisions.
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS max_instances integer NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'deployments_max_instances_chk'
       AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_max_instances_chk
      CHECK (max_instances >= 0);
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN deployments.max_instances IS
  'Immutable per-deployment serving-instance ceiling; zero inherits the app ceiling.';

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_max_instances_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS max_instances;
