-- filename: 20260911224616245_deployments_rollout_state_repair.sql

-- +goose Up
-- +goose StatementBegin

-- Issue #1926 / SAFE-RELEASES: a stable deployment has no canary ladder and
-- is terminal as soon as it becomes live. Older write paths left those rows
-- at the migration default (`pending`), which made deployment history and
-- rollout recovery disagree about whether a rollout was still actionable.
-- Repair existing rows once, while leaving readiness-gated service rollouts
-- (`rolling_out` with zero canary steps) untouched.
UPDATE deployments
   SET rollout_state = 'complete',
       rollout_completed_at = COALESCE(rollout_completed_at, NOW())
 WHERE status = 'live'
   AND canary_total_steps = 0
   AND coalesce(nullif(rollout_state, ''), 'pending') = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The repair is intentionally not reversed. Reverting it would recreate the
-- false in-flight state this migration removes and could make live rows
-- actionable by rollout recovery again.

-- +goose StatementEnd
