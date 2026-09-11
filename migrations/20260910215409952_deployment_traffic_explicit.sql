-- +goose Up
-- Preserve the distinction between an omitted traffic_percent (stable 100%
-- rollout) and an explicitly requested 0%. The prior Go zero-value fallback
-- collapsed both cases and could unexpectedly replace the serving revision.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS traffic_percent_explicit boolean NOT NULL DEFAULT false;

-- Manual traffic splits intentionally keep more than one revision live in a
-- scope. Exclude those explicitly weighted rows from the stable-row uniqueness
-- guard, while retaining the existing canary and service-rollout behavior.
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;
CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live'
      AND traffic_percent_explicit = false
      AND (canary_total_steps = 0 AND rollout_state <> 'rolling_out'
           OR rollout_state = 'complete');

-- +goose Down
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;
CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live'
      AND (canary_total_steps = 0 AND rollout_state <> 'rolling_out'
           OR rollout_state = 'complete');

ALTER TABLE deployments
    DROP COLUMN IF EXISTS traffic_percent_explicit;
