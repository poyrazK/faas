-- filename: 00348_alert_presets_seed.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 — seed the 8 alert preset catalog rows.
--
-- The catalog is fixed at 8 entries. Customers cannot mutate the
-- rows; the system-owner role used by meterd + apid at boot is the
-- only writer. A future preset lands as a new INSERT here plus an
-- extension to the evaluator signal source. The PR for a new
-- preset MUST include:
--   1. the row below
--   2. an `enabled_in_catalog = false` flip when the backing
--      signal is not yet plumbed (default state for the 5 rows
--      whose signals land in PR-A's signal-plumbing work)
--   3. an evaluator case in pkg/alerts/evaluator.go::observe
--   4. a state.AlertMetric* constant in pkg/state/types.go
--   5. an entry in pkg/api.AllowedAlertRuleMetrics
--
-- Replay-safety: every INSERT uses ON CONFLICT (name) DO NOTHING
-- so a re-run of MigrateUp is a no-op. The same pattern is used
-- in migration 00111 (consumer_keys seed) and the per-feature
-- catalog seeds elsewhere.

INSERT INTO alert_presets (
    name, display_name, description, category,
    metric, comparison, threshold, window_spec,
    default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES
    -- reliability: error rate
    (
        'error_rate_2pct',
        'Error rate exceeds 2%',
        'Fires when the rolling 15-minute error rate for the app exceeds 2 percent. Use this to catch a wave of 5xx before customers notice.',
        'reliability',
        'error_rate_pct', 'gt', 2.0, '15m',
        15, true, 'hobby'
    ),
    -- reliability: p95 latency
    (
        'p95_latency_1s',
        'p95 exceeds one second',
        'Fires when the 95th-percentile invocation latency over the last 15 minutes exceeds 1 second. Useful for catching latency regressions before they cascade.',
        'reliability',
        'latency_p95_ms', 'gt', 1000.0, '15m',
        15, true, 'hobby'
    ),
    -- reliability: cold starts
    (
        'cold_start_10pct',
        'Cold starts exceed 10%',
        'Fires when more than 10 percent of invocations over the last hour are cold starts (parked-snapshot wake). Indicates traffic is bouncing the wake-queue cap.',
        'reliability',
        'cold_start_pct', 'gt', 10.0, '1h',
        30, true, 'hobby'
    ),
    -- availability: API down (lands with signal in PR-A; enabled_in_catalog flips to true when meterd_api_reachable is wired)
    (
        'api_down',
        'API is down',
        'Fires when the app has not served a successful request in the last 5 minutes. Backed by the meterd reachability probe.',
        'availability',
        'api_up', 'lt', 1.0, '5m',
        5, false, 'pro'
    ),
    -- cost: spend MTD
    (
        'spend_eur_20',
        'Spend exceeds €20',
        'Fires when the account''s month-to-date spend exceeds €20. Backed by the meterd MTD billing aggregator (plan RAM + 8 MB per running second; rate from pkg/api/limits.go).',
        'cost',
        'account_spend_eur', 'gt', 20.0, '24h',
        60, false, 'hobby'
    ),
    -- deployment: deployment failed
    (
        'deploy_failed',
        'Deployment failed',
        'Fires when any deployment for this app lands in the failed state during the last hour. Backed by the deployments.status = ''failed'' Postgres scan.',
        'deployment',
        'deployment_failed', 'gt', 0.0, '1h',
        15, false, 'pro'
    ),
    -- infrastructure: cert expiring (14d window)
    (
        'cert_expiring_14d',
        'Domain certificate is expiring',
        'Fires when any of the app''s domain certificates has fewer than 14 days remaining. Backed by the apid_tenant_surface_cert_expiry refresher walker.',
        'infrastructure',
        'cert_expiry_seconds', 'lt', 1209600.0, '24h',
        1440, false, 'hobby'
    ),
    -- infrastructure: queue backlog
    (
        'queue_backlog_growing',
        'Queue backlog is increasing',
        'Fires when the per-app wake queue depth exceeds 50 waiters over the last 15 minutes. Backed by the existing gateway_queue_depth{app} gauge.',
        'infrastructure',
        'queue_depth', 'gt', 50.0, '15m',
        15, false, 'scale'
    )
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only seed. Reverse deletes the seeded rows by name. New
-- presets added after this migration must include their own DELETE
-- line in the Down block.
DELETE FROM alert_presets WHERE name IN (
    'error_rate_2pct', 'p95_latency_1s', 'cold_start_10pct',
    'api_down', 'spend_eur_20', 'deploy_failed',
    'cert_expiring_14d', 'queue_backlog_growing'
);

-- +goose StatementEnd