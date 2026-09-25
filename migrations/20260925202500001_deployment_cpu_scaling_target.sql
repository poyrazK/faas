-- A deployment may override the app's CPU utilization target. NULL inherits
-- the app policy; zero explicitly disables CPU-based scale-up for a revision.
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS cpu_utilization_target_pct double precision;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'deployments_cpu_utilization_target_pct_chk'
       AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_cpu_utilization_target_pct_chk
      CHECK (cpu_utilization_target_pct IS NULL OR
             (cpu_utilization_target_pct >= 0 AND cpu_utilization_target_pct <= 100));
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN deployments.cpu_utilization_target_pct IS
  'Optional per-deployment CPU scale-up target percentage; NULL inherits app policy, zero disables CPU scale-up.';

CREATE INDEX IF NOT EXISTS deployments_live_cpu_scaling_override_idx
  ON deployments (app_id, created_at DESC)
  WHERE status = 'live' AND cpu_utilization_target_pct IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS deployments_live_cpu_scaling_override_idx;
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_cpu_utilization_target_pct_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS cpu_utilization_target_pct;
