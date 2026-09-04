-- filename: 20260904201500000_canary_scope_overlap.sql
-- +goose Up
-- +goose StatementBegin

-- SAFE-RELEASES (issue #976 / ADR-122): an active canary is a deliberate
-- temporary overlap of two live revisions in the same environment scope.
-- Keep the one-live-per-scope invariant for stable rows, while allowing the
-- canary row to coexist with its predecessor until the atomic terminal step
-- supersedes the predecessor.
DROP INDEX IF EXISTS deployments_app_scope_live_uniq;

CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live'
      AND (canary_total_steps = 0 OR rollout_state = 'complete');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS deployments_app_scope_live_uniq;

CREATE UNIQUE INDEX deployments_app_scope_live_uniq
    ON deployments (app_id, scope)
    WHERE status = 'live';

-- +goose StatementEnd
