-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-745 slice 4: a custom_metric alert names one pushed metric
-- (app_custom_metrics.name) and is app-scoped, webhook-only, and limited
-- to windows the metric's Prometheus history can answer meaningfully.
-- +goose StatementBegin
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS custom_metric_name text;
DO $$
DECLARE base text;
BEGIN
    -- Additive (as #4877): OR this migration's metrics onto whatever the
    -- constraint allows now, so metrics other migrations added survive
    -- replays and out-of-order application. Down restores the stored base.
    SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
    IF strpos(base, '''custom_metric''') = 0 THEN
        EXECUTE format('CREATE OR REPLACE FUNCTION alert_rules_custom_metric_metric_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L', format('SELECT %L::text', base));
        ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
        EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''custom_metric''))', regexp_replace(base, '^CHECK \((.*)\)$', '\1'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_custom_metric_chk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_custom_metric_chk CHECK (((metric = 'custom_metric') = (custom_metric_name IS NOT NULL)) AND (metric <> 'custom_metric' OR (app_id IS NOT NULL AND event_subscription_id IS NULL AND action = 'webhook' AND window_spec IN ('5m','15m','1h','6h','24h') AND custom_metric_name ~ '^[a-z][a-z0-9_]{0,62}$')));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE metric='custom_metric') THEN RAISE EXCEPTION 'delete custom_metric alert rules before rollback'; END IF; END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_custom_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
DO $$ BEGIN EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || alert_rules_custom_metric_metric_base(); END $$;
DROP FUNCTION alert_rules_custom_metric_metric_base();
ALTER TABLE alert_rules DROP COLUMN custom_metric_name;
-- +goose StatementEnd
