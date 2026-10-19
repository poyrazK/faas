-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-746: alert on guest log lines vmmd classified as error or warn.
-- +goose StatementBegin
DO $$
DECLARE base text;
BEGIN
    -- Additive (as #4877): OR this migration's metrics onto whatever the
    -- constraint allows now, so metrics other migrations added survive
    -- replays and out-of-order application. Down restores the stored base.
    SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
    IF strpos(base, '''log_error_lines''') = 0 THEN
        EXECUTE format('CREATE OR REPLACE FUNCTION alert_rules_log_lines_metric_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L', format('SELECT %L::text', base));
        ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
        EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''log_error_lines'',''log_warn_lines''))', regexp_replace(base, '^CHECK \((.*)\)$', '\1'));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('log_error_lines','log_warn_lines')) THEN RAISE EXCEPTION 'delete log line alert rules before rollback'; END IF; END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
DO $$ BEGIN EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || alert_rules_log_lines_metric_base(); END $$;
DROP FUNCTION alert_rules_log_lines_metric_base();
-- +goose StatementEnd
