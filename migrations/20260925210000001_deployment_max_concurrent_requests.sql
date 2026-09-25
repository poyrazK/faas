-- A deployment may lower the maximum number of simultaneous requests served
-- by each of its instances. NULL inherits the plan's per-instance limit.
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS max_concurrent_requests integer;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'deployments_max_concurrent_requests_chk'
       AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_max_concurrent_requests_chk
      CHECK (max_concurrent_requests IS NULL OR
             (max_concurrent_requests >= 1 AND max_concurrent_requests <= 1000));
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN deployments.max_concurrent_requests IS
  'Optional immutable per-revision hard request cap per instance; NULL inherits the plan limit.';

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_max_concurrent_requests_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS max_concurrent_requests;
