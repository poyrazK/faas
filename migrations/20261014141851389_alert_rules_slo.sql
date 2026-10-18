-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-747 slice 3: alert rules on a customer SLO. slo_id is set exactly for
-- the two SLO metrics, which are app-scoped; deleting the SLO deletes them.
-- +goose StatementBegin
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS slo_id uuid REFERENCES app_slos(id) ON DELETE CASCADE;
DO $$
DECLARE base text;
BEGIN
    -- Additive (as #4877): OR this migration's metrics onto whatever the
    -- constraint allows now, so metrics other migrations added survive
    -- replays and out-of-order application. Down restores the stored base.
    SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
    IF strpos(base, '''slo_budget_burn''') = 0 THEN
        EXECUTE format('CREATE OR REPLACE FUNCTION alert_rules_slo_metric_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L', format('SELECT %L::text', base));
        ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
        EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''slo_budget_burn'',''slo_budget_remaining_pct''))', regexp_replace(base, '^CHECK \((.*)\)$', '\1'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_slo_chk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_slo_chk CHECK ((((metric = ANY (ARRAY['slo_budget_burn'::text, 'slo_budget_remaining_pct'::text])) = (slo_id IS NOT NULL)) AND ((slo_id IS NULL) OR (app_id IS NOT NULL))));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE slo_id IS NOT NULL) THEN RAISE EXCEPTION 'delete SLO alert rules before rollback'; END IF; END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_slo_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
DO $$ BEGIN EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || alert_rules_slo_metric_base(); END $$;
DROP FUNCTION alert_rules_slo_metric_base();
DROP INDEX IF EXISTS alert_rules_slo_id_idx;
ALTER TABLE alert_rules DROP COLUMN slo_id;
-- +goose StatementEnd
