-- filename: 20260904205349285_compute_nodes_recovery_audit.sql
-- Workstream B (issue #1184): add the recovery-tracking columns to
-- compute_nodes so the operator runbook, audit dashboard, and
-- `node.draining` / `node.drained` / `node.failed` / `node.recovered`
-- events can correlate wall-clock timestamps without joins to
-- compute_node_heartbeats.
--
-- All four columns are nullable because:
--  - drain_initiated_at / drain_completed_at: lifecycle='draining' flips
--    before drain completes; completed_at stamped when arbiter confirms
--    zero live instances.
--  - recovery_initiated_at / last_recovery_outcome: only stamped by the
--    recovery arbiter after a successful migrate-or-recreate sweep.
--
-- last_recovery_outcome CHECK is a closed set so an
-- external `UPDATE compute_nodes SET last_recovery_outcome=...` is
-- rejected at the DB boundary (closes an ops typo footgun).
-- +goose Up
-- +goose StatementBegin
ALTER TABLE compute_nodes
    ADD COLUMN IF NOT EXISTS drain_initiated_at       timestamptz NULL,
    ADD COLUMN IF NOT EXISTS drain_completed_at       timestamptz NULL,
    ADD COLUMN IF NOT EXISTS recovery_initiated_at    timestamptz NULL,
    ADD COLUMN IF NOT EXISTS last_recovery_outcome    text NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'compute_nodes_last_recovery_outcome_chk'
    ) THEN
        ALTER TABLE compute_nodes
            ADD CONSTRAINT compute_nodes_last_recovery_outcome_chk
            CHECK (last_recovery_outcome IS NULL OR
                   last_recovery_outcome IN ('succeeded','failed','partial'));
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE compute_nodes
    DROP CONSTRAINT IF EXISTS compute_nodes_last_recovery_outcome_chk;
ALTER TABLE compute_nodes
    DROP COLUMN IF EXISTS last_recovery_outcome,
    DROP COLUMN IF EXISTS recovery_initiated_at,
    DROP COLUMN IF EXISTS drain_completed_at,
    DROP COLUMN IF EXISTS drain_initiated_at;
-- +goose StatementEnd
