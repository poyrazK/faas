-- filename: 20260905000000003_service_rollout_overlap.sql
-- +goose Up
-- +goose StatementBegin

-- Readiness-gated service rollouts deliberately keep the predecessor live
-- while the new zero-step generation is warming. Reuse the existing rollout
-- state column as the marker and exclude only that marker from the one-live
-- scope index. Stable zero-step rows remain protected, and active canaries
-- retain the overlap behavior introduced by the preceding migration.
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;

CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live'
      AND (canary_total_steps = 0 AND rollout_state <> 'rolling_out'
           OR rollout_state = 'complete');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS deployments_app_scope_live_uniq;

CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live';

-- +goose StatementEnd
