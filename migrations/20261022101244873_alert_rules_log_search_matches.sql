-- filename: 20261022101244873_alert_rules_log_search_matches.sql
-- migration-compat: expand
-- ADR-20261018083536 slice D: log_search_matches counts an app's log events
-- matching the rule's stored filters (log_query, the canonical query string
-- of a saved search) within the rule window. Like custom_metric, the rule is
-- app-scoped and webhook-only, with windows up to 24 hours.
--
-- alert_rules_log_search_chk is added NOT VALID and validated first. The
-- metric CHECK is then replaced online: a widened copy of its current
-- definition (which keeps metrics other migrations added) is added NOT VALID
-- under a new name and validated, the old one is dropped, the old name is
-- re-added from the copy NOT VALID and validated, and the copy is dropped.
-- Every step commits on its own and is guarded, so writers never wait on a
-- scan and a rerun after a partial failure converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS log_query text;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_log_search_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_log_search_chk CHECK (((metric = 'log_search_matches') = (log_query IS NOT NULL))
    AND ((metric <> 'log_search_matches') OR ((app_id IS NOT NULL) AND (event_subscription_id IS NULL) AND (action = 'webhook')
      AND (window_spec IN ('5m', '15m', '1h', '6h', '24h')) AND (octet_length(log_query) <= 2048)))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_log_search_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_log_search_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('log_search_matches' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_log_search_chk CHECK ((%s) OR metric = %L) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'log_search_matches');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_log_search_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_log_search_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_log_search_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_log_search_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_log_search_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE now_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'log_search_matches') THEN
  RAISE EXCEPTION 'delete log_search_matches alert rules before rollback';
 END IF;
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_log_search_chk;
 SELECT pg_get_constraintdef(oid) INTO now_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(' OR (metric = ''log_search_matches''::text)' in now_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(now_def, ' OR (metric = ''log_search_matches''::text)', '');
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules DROP COLUMN IF EXISTS log_query;
RESET lock_timeout;
