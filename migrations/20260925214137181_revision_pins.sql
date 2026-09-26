-- +goose Up
-- Zero-traffic retained revisions must not collide with the one-active-stable
-- guard. Keep the guard for traffic-bearing stable deployments.
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live' AND traffic_percent > 0
      AND traffic_percent_explicit = false
      AND (canary_total_steps = 0 AND rollout_state <> 'rolling_out'
           OR rollout_state = 'complete');

-- A replaced revision stays live at zero traffic while its client pin is
-- valid. A subsequent deploy never resets the deadline. Replacing a project
-- release set may extend it to cover that graph's compatibility window.
CREATE TABLE IF NOT EXISTS deployment_revision_pins (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS deployment_revision_pins_expiry_idx ON deployment_revision_pins (expires_at);
CREATE INDEX IF NOT EXISTS deployment_revision_pins_app_idx ON deployment_revision_pins (app_id, expires_at DESC);

-- +goose Down
DROP TABLE IF EXISTS deployment_revision_pins;
UPDATE deployments SET status = 'superseded'
 WHERE status = 'live' AND traffic_percent = 0
   AND traffic_percent_explicit = false;
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live'
      AND traffic_percent_explicit = false
      AND (canary_total_steps = 0 AND rollout_state <> 'rolling_out'
           OR rollout_state = 'complete');
