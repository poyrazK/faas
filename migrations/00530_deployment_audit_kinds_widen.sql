-- filename: 00530_deployment_audit_kinds_widen.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #976 / ADR-122 / SAFE-RELEASES-OBS (PR-A) — widen the
-- deployment_audit_kind_chk closed set to admit the orchestrator
-- emit surface (deploy.rollout_started / completed / aborted) plus
-- the canary-step-advanced + alert-rule-fired kinds planned by
-- PR-D. The original closed set (8 kinds, migrations/00477_deployment_audit.sql)
-- predates Mega PR #2's orchestrator goroutine; the orchestrator's
-- emitAudit calls would have hit SQLSTATE 23514 silently because
-- the state-machine write landed regardless — exactly the silent
-- soak-bypass the audit trail exists to prevent. This migration
-- closes the gap so the orchestrator's audit rows are accepted
-- into the table.
--
-- New kinds admitted (5 total):
--   deploy.rollout_started    — pkg/safedeploy.Orchestrator.start
--   deploy.rollout_completed  — pkg/safedeploy.Orchestrator.complete
--   deploy.rollout_aborted    — pkg/safedeploy.Orchestrator.abort
--                                (manual CLI, not used by orchestrator today
--                                but the constant is declared)
--   deploy.canary_step_advanced — pkg/canary.Progression (PR-D new)
--   deploy.alert_rule_fired   — pkg/alerts.ActionDispatcher (PR-D new)
--
-- Replay safety: DROP CONSTRAINT IF EXISTS + ADD CONSTRAINT is
-- metadata-only on PG11+ (no row rewrite). The new CHECK admits
-- every value the old CHECK admitted, so re-applying against a
-- pre-PR deployment_audit table is a no-op semantically. The kind
-- widening does NOT change the table's column shape, so existing
-- read paths (ListDeploymentAudit, dashboard render) continue to
-- work without a wire-additive bump.
--
-- Two index additions ship in this migration because both feed
-- PR-A's Prometheus counter call sites:
--
--   deployment_audit_account_at_idx — backs the per-account
--     timeline view that PR-C's /dashboard/safe-releases handler
--     renders. Partial index keeps the storage cost bounded to
--     account-scoped rows (fleet-level orchestrator emits with
--     account_id IS NULL are excluded — they're the meterd:safedeploy
--     sentinel rows that the dashboard surfaces under the "fleet"
--     group, not per-account).
--
--   deployments_canary_step_started_at_idx — backs the stalled-
--     canary query that PR-B's `canary_stuck_step` Prometheus
--     alert evaluates. The WHERE rollout_state = 'rolling_out'
--     predicate keeps the index small (only rolling-out rows
--     participate; complete/aborted rows are excluded). The
--     Orchestrator's per-tick walk in pkg/safedeploy/orchestrator.go
--     uses the same predicate, so the index also feeds the
--     in-process Stats.StuckDetected counter.

ALTER TABLE deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit
    ADD CONSTRAINT deployment_audit_kind_chk
        CHECK (kind IN (
            'deploy.created',
            'deploy.source_ref',
            'deploy.local_tarball',
            'deploy.traffic_changed',
            'deploy.health_probe_failed',
            'deploy.health_recovered',
            'deploy.rolled_back',
            'deploy.removed',
            -- PR-A: orchestrator emit surface.
            'deploy.rollout_started',
            'deploy.rollout_completed',
            'deploy.rollout_aborted',
            -- PR-D: alert-rule + canary-step audit kinds.
            'deploy.canary_step_advanced',
            'deploy.alert_rule_fired'
        ));

CREATE INDEX CONCURRENTLY IF NOT EXISTS deployment_audit_account_at_idx
    ON deployment_audit (account_id, at DESC)
    WHERE account_id IS NOT NULL;

-- Cannot wrap CONCURRENTLY inside a transaction block; goose
-- StatementBegin/End default to autocommit, so each DDL runs in
-- its own implicit transaction and CONCURRENTLY is permitted.
CREATE INDEX CONCURRENTLY IF NOT EXISTS deployments_canary_step_started_at_idx
    ON deployments (canary_step_started_at)
    WHERE rollout_state = 'rolling_out';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS deployments_canary_step_started_at_idx;
DROP INDEX IF EXISTS deployment_audit_account_at_idx;
ALTER TABLE IF EXISTS deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
-- Recreate the original 8-kind closed set (matches 00477's
-- pre-PR-A shape). Pre-A rows whose kind ∈ {rollout_started,
-- rollout_completed, rollout_aborted, canary_step_advanced,
-- alert_rule_fired} will fail the new CHECK on read; that's the
-- desired effect of the rollback — a Down is a destructive
-- operation that surfaces the kind-set mismatch.
ALTER TABLE IF EXISTS deployment_audit
    ADD CONSTRAINT deployment_audit_kind_chk
        CHECK (kind IN (
            'deploy.created',
            'deploy.source_ref',
            'deploy.local_tarball',
            'deploy.traffic_changed',
            'deploy.health_probe_failed',
            'deploy.health_recovered',
            'deploy.rolled_back',
            'deploy.removed'
        ));

-- +goose StatementEnd
