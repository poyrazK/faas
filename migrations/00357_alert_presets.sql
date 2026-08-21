-- filename: 00357_alert_presets.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 — one-click alert presets. Today every
-- alert rule has to be authored by hand with a (metric, comparison,
-- threshold, window_spec) quadruple, which forces customers to
-- understand Prometheus semantics to ship a basic alert. The
-- preset catalog stands up a fixed set of well-known conditions
-- (API down, error rate > 2%, p95 > 1 s, cold starts > 10%, spend
-- > €20, deployment failed, domain cert expiring, queue backlog
-- growing) that a non-engineer can enable with a single click.
--
-- Storage shape:
--   - name is the stable catalog key ('api_down', 'p95_latency_1s',
--     etc). UNIQUE — no two presets share a name. The CLI uses it
--     as the positional argument for `gregale alerts preset enable`.
--   - display_name / description / category render in the dashboard
--     catalog panel and CLI help. category buckets the dashboard
--     grid into availability / reliability / cost / deployment /
--     infrastructure columns.
--   - metric / comparison / threshold / window_spec mirror the
--     alert_rules closed vocabularies byte-for-byte. The CHECK
--     below pins the catalog's metric vocabulary to the same eight
--     strings the evaluator learns in migration 00359; a future
--     metric lands here first, then in 00359, then in
--     pkg/api.AllowedAlertRuleMetrics + pkg/state.AlertMetric* —
--     in that order.
--   - default_cooldown_minutes seeds the instantiated alert_rules
--     row's cooldown. The customer can override via the optional
--     cooldown_minutes field on POST .../enable.
--   - enabled_in_catalog is the customer-facing "is this preset
--     clickable?" flag. PR-A ships 3 enabled rows
--     (error_rate_2pct, p95_latency_1s, cold_start_10pct) and 5
--     disabled rows (api_down, spend_eur_20, deploy_failed,
--     cert_expiring_14d, queue_backlog_growing) that flip to
--     enabled as their backing signals land in meterd.
--   - minimum_plan gates the preset below the customer's plan tier
--     (mirrors the alert-rules plan gate at cmd/apid/handlers_alerts.go:102-105).
--     A Free plan never sees the catalog; a Hobby plan sees Hobby+
--     rows; etc.
--
-- Catalog mutability: customers have SELECT-only. INSERT / UPDATE /
-- DELETE are reserved for the meterd + apid system-owner role at
-- boot. The hand-written pgstore queries in pkg/state/pgstore.go
-- never expose a mutator method for alert_presets — the only path
-- in is migration 00358's idempotent seed.
--
-- Replay-safety: CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT
-- EXISTS, CREATE OR REPLACE FUNCTION, DROP TRIGGER IF EXISTS +
-- CREATE TRIGGER. Mirrors migration 00304's pattern verbatim.

CREATE TABLE IF NOT EXISTS alert_presets (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                     text NOT NULL,
    display_name             text NOT NULL,
    description              text NOT NULL,
    category                 text NOT NULL,
    metric                   text NOT NULL,
    comparison               text NOT NULL,
    threshold                double precision NOT NULL,
    window_spec              text NOT NULL,
    default_cooldown_minutes int NOT NULL DEFAULT 15,
    enabled_in_catalog       boolean NOT NULL DEFAULT true,
    minimum_plan             text NOT NULL DEFAULT 'hobby',
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT alert_presets_name_uniq UNIQUE (name),
    CONSTRAINT alert_presets_name_len_chk CHECK (char_length(name) BETWEEN 1 AND 64),
    CONSTRAINT alert_presets_display_name_len_chk CHECK (char_length(display_name) BETWEEN 1 AND 128),
    CONSTRAINT alert_presets_description_len_chk CHECK (char_length(description) BETWEEN 1 AND 512),
    CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
        'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
        'api_up', 'account_spend_eur', 'deployment_failed',
        'cert_expiry_seconds', 'queue_depth'
    )),
    CONSTRAINT alert_presets_comparison_chk CHECK (comparison IN ('gt','gte','lt','lte')),
    CONSTRAINT alert_presets_window_chk CHECK (window_spec IN
        ('5m','15m','1h','6h','24h','7d','15d')),
    CONSTRAINT alert_presets_category_chk CHECK (category IN
        ('availability','reliability','cost','deployment','infrastructure')),
    CONSTRAINT alert_presets_plan_chk CHECK (minimum_plan IN
        ('free','hobby','pro','scale')),
    CONSTRAINT alert_presets_cooldown_chk CHECK (
        default_cooldown_minutes BETWEEN 5 AND 1440
    )
);

-- Catalog hot read path is enabled_in_catalog = true ordered by
-- category, name. The partial index keeps disabled rows out of the
-- dashboard grid scan.
CREATE INDEX IF NOT EXISTS alert_presets_enabled_idx
    ON alert_presets (enabled_in_catalog, category, name)
    WHERE enabled_in_catalog = true;

-- updated_at trigger. Inline declaration — same precedent as
-- migrations/00304_cors_presets.sql:83-91. We do not reuse the
-- cors_presets_set_updated_at function because its name is
-- misleading for a non-cors_presets table; a future shared helper
-- can collapse these when a third table lands.
CREATE OR REPLACE FUNCTION alert_presets_set_updated_at()
    RETURNS trigger
    LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS alert_presets_set_updated_at_trg ON alert_presets;
CREATE TRIGGER alert_presets_set_updated_at_trg
    BEFORE UPDATE ON alert_presets
    FOR EACH ROW
    EXECUTE FUNCTION alert_presets_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only migration. Drop order: trigger, function, indexes,
-- table. The seed in 00358 is reversed by the table drop.
DROP TRIGGER IF EXISTS alert_presets_set_updated_at_trg ON alert_presets;
DROP FUNCTION IF EXISTS alert_presets_set_updated_at();
DROP INDEX IF EXISTS alert_presets_enabled_idx;
DROP TABLE IF EXISTS alert_presets;

-- +goose StatementEnd