-- filename: 00370_alert_rules_extend_metrics_chk.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 — extend alert_rules_metric_chk to allow
-- the 5 new metric strings that the preset catalog introduces.
-- The catalog seed in 00369 references these metrics; the
-- evaluator's `observe` dispatch learns them in the same PR; the
-- pkg/api.AllowedAlertRuleMetrics + pkg/state.AlertMetric*
-- closed sets learn them in the same PR. The DB CHECK is the
-- load-bearing gate so a future drift cannot bypass the closed
-- vocabulary by skipping one layer.
--
-- The new strings:
--   api_up              — 0/1 reachability; binary metric
--   account_spend_eur   — EUR cents MTD; computed by meterd
--   deployment_failed   — count over window; Postgres scan
--   cert_expiry_seconds — remaining seconds on per-app domain cert
--   queue_depth         — gateway wake queue depth per app
--
-- DROP + ADD is the canonical Postgres pattern for changing a
-- CHECK constraint. The constraint is unnamed in 00062_alert_rules.sql
-- (Postgres synthesised name alert_rules_metric_chk); the explicit
-- IF EXISTS guards against re-run idempotency.

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

-- +goose Down
-- +goose StatementBegin

-- Reverse: restore the original 7-string vocabulary. Fails if any
-- live row references one of the new metric strings (deliberate —
-- forces the operator to clean up before reverse).
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count',
    'failed_invocations'
));

-- +goose StatementEnd