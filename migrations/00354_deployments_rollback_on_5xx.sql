-- +goose Up
-- +goose StatementBegin
-- Mega-C PR-2 / issue #961 leaf 8: auto-rollback on first-N-5xx
-- within the first deploy window (Pro+). The customer opts in
-- per-deploy via deployments.rollback_on_5xx (boolean, default
-- false). Once opted in, the per-deploy first_5xx_count is
-- incremented on every 5xx response observed by the gateway
-- (the new wake.response_5xx event kind carries
-- {app_id, deployment_id, status_code, wake_id, request_id}).
-- When the counter crosses the per-plan threshold inside
-- first_5xx_window_ends_at, schedd calls the apid-internal
-- /v1/internal/auto-rollback-on-5xx endpoint which reuses the
-- existing rollback() handler (status swap live ⇄ superseded)
-- and emits the same app.rolled_back audit row with
-- trigger="auto_5xx".
--
-- Per ADR-041 (slot discipline) slot 348 is the next free slot
-- above PR-1's 00347. PRs #1030/#1034/#1036 fence slots up to
-- 00352, so this branch renumbered to 00353/00354 past that
-- range to dodge the cross-PR slot collision. See
-- memory: cross-pr-slot-precheck-pr-867-collision-2026-08-13.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS rollback_on_5xx            BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS first_wake_at              TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS first_5xx_window_ends_at   TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS first_5xx_count            INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_auto_rollback_at      TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS last_auto_rollback_reason  TEXT        NULL;

-- Closed-set vocabulary for last_auto_rollback_reason (mirrors
-- the deployments_parked_reason_check pattern at 00157).
-- Explicit tripwire against a typo in the schedd emit path.
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_last_auto_rollback_reason_check;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_last_auto_rollback_reason_check
    CHECK (last_auto_rollback_reason IS NULL
        OR last_auto_rollback_reason IN ('threshold_exceeded', 'first_window_expired'));

-- Partial index on the schedd-side /auto-rollback scan. Schedd
-- scans (rollback_on_5xx=true AND first_5xx_count >= N AND
-- first_5xx_window_ends_at > now()) on every wake.response_5xx
-- event; the partial index keeps the scan narrow.
CREATE INDEX IF NOT EXISTS deployments_rollback_on_5xx_pending_idx
    ON deployments (app_id, first_5xx_count)
    WHERE rollback_on_5xx = true
      AND first_5xx_window_ends_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS deployments_rollback_on_5xx_pending_idx;
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_last_auto_rollback_reason_check;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS last_auto_rollback_reason,
    DROP COLUMN IF EXISTS last_auto_rollback_at,
    DROP COLUMN IF EXISTS first_5xx_count,
    DROP COLUMN IF EXISTS first_5xx_window_ends_at,
    DROP COLUMN IF EXISTS first_wake_at,
    DROP COLUMN IF EXISTS rollback_on_5xx;
-- +goose StatementEnd