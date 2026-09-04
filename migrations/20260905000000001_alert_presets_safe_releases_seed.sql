-- filename: 20260905000000001_alert_presets_safe_releases_seed.sql
-- +goose Up
-- +goose StatementBegin

-- SAFE-RELEASES-OBS PR-B (issue #976 / ADR-122): Prometheus alert
-- rules for the canary + safedeploy lifecycle. Mirrors the
-- migration 00419_alert_rules_extend_metrics_chk.sql widening
-- pattern + the migration 00418_alert_presets_seed.sql catalog
-- seed pattern. This migration uses the first timestamp slot after
-- the repository's 20260904 cutover; its predecessor is the PR-A
-- migration 20260905000000000_deployment_audit_kinds_widen.sql.
--
-- Three parts:
--   1. Widen alert_rules_metric_chk to admit the 4 new metric
--      strings. The DB CHECK is the load-bearing gate so a future
--      drift cannot bypass the closed vocabulary by skipping one
--      layer (same precedent as 00419).
--   2. Widen alert_presets_metric_chk to admit the same 4 strings.
--      Migration 00417 originally admitted only 8 (catalog seed
--      inserts); PR-B extends the catalog to 12.
--   3. Insert 4 new alert_presets rows that surface in the
--      /dashboard/alerts grid. enabled_in_catalog = true for the
--      two whose backing signals are plumbed end-to-end; false for
--      the other two which still need a webhook receiver (PR-D
--      follow-up).
--
-- Replay-safety: every INSERT uses ON CONFLICT (name) DO NOTHING
-- so a re-run of MigrateUp is a no-op. CHECK widenings use DROP +
-- ADD with IF EXISTS guards, same as 00419.

-- (1) widen alert_rules_metric_chk
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'queue_depth',
    -- SAFE-RELEASES-OBS PR-B (issue #976 / ADR-122)
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

-- (2) widen alert_presets_metric_chk
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;

ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
    'api_up', 'account_spend_eur', 'deployment_failed',
    'cert_expiry_seconds', 'queue_depth',
    -- SAFE-RELEASES-OBS PR-B (issue #976 / ADR-122)
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

-- (3) seed 4 new alert_presets rows
INSERT INTO alert_presets (
    name, display_name, description, category,
    metric, comparison, threshold, window_spec,
    default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES
    -- canary: stuck step (operator tripwire)
    (
        'canary_stuck_step',
        'Canary stuck at same step',
        'Fires when the safedeploy orchestrator detects a rollout stuck at the same canary step past StuckAfterDuration (default 30 min). Backed by safedeploy_orchestrator_stuck_detected_total rate > 0 for 5 min. Operator CLI: gregale rollouts recover <slug>.',
        'deployment',
        'canary_stuck_step', 'gt', 0.0, '5m',
        15, true, 'scale'
    ),
    -- canary: audit emit failing (critical; closes the audit-trail-blacked-out failure mode PR-A unblocked)
    (
        'safedeploy_audit_emit_failing',
        'Safedeploy audit emit failing',
        'Fires when safedeploy_orchestrator_audit_emit_failed_total rate > 0.1/sec for 10 min. Critical. Likely cause: deployment_audit_kind_chk widened-missed (PR-A closes this); the underlying kind widening must have shipped before this alert can stay quiet.',
        'deployment',
        'safedeploy_audit_emit_failing', 'gt', 0.1, '10m',
        15, true, 'scale'
    ),
    -- infrastructure: 90-day audit GC failing
    (
        'deployment_audit_gc_failing',
        'Deployment audit GC failing',
        'Fires when deployment_audit_gc_failed_total rate > 0 for 1 h. Warning. The 90-day deployment_audit retention cron is failing; disk-fill risk. Backed by pkg/meter/retention.go::RetentionOnceDeploymentAudit onTickError.',
        'infrastructure',
        'deployment_audit_gc_failing', 'gt', 0.0, '1h',
        60, true, 'scale'
    ),
    -- canary: fleet in-flight high (operator back-pressure)
    (
        'canary_fleet_in_flight_high',
        'Canary fleet in-flight high',
        'Fires when safedeploy_in_flight_rollouts > 50 for 10 min. Warning. Operator back-pressure signal: more than 50 canaries in flight fleet-wide. Backed by pkg/safedeploy/orchestrator.go::Once gauge sample after SafedeployListPendingRollouts.',
        'deployment',
        'canary_fleet_in_flight_high', 'gt', 50.0, '10m',
        15, true, 'scale'
    )
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only seed. Reverse deletes the 4 seeded rows by name
-- AND rolls back the CHECK widenings. New presets added after
-- this migration must include their own DELETE line in the Down
-- block.
DELETE FROM alert_presets WHERE name IN (
    'canary_stuck_step', 'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing', 'canary_fleet_in_flight_high'
);

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
    'api_up', 'account_spend_eur', 'deployment_failed',
    'cert_expiry_seconds', 'queue_depth'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'queue_depth'
));

-- +goose StatementEnd
