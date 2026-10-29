-- filename: 20261027134058412_alert_rules_queue_backlog.sql
-- migration-compat: expand
-- ADR-20261018101500 amendment 2026-10-27: queue_backlog and
-- queue_oldest_age_seconds alert on an app's durable queue (the messages
-- waiting for its workers and how long the oldest has waited), from the
-- schedd queue gauges. Both are app-scoped and webhook-only. queue_depth
-- keeps its meaning: requests the gateway holds while an instance wakes.
--
-- alert_rules_queue_backlog_chk is added NOT VALID and validated first. The
-- metric CHECK is then replaced online as in
-- 20261022101244873_alert_rules_log_search_matches.sql: a widened copy of its
-- current definition is added NOT VALID under a new name and validated, the
-- old one is dropped, the old name is re-added from the copy NOT VALID and
-- validated, and the copy is dropped. Every step commits on its own and is
-- guarded, so writers never wait on a scan and a rerun converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_queue_backlog_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_queue_backlog_chk CHECK (metric NOT IN ('queue_backlog', 'queue_oldest_age_seconds')
    OR (app_id IS NOT NULL AND event_subscription_id IS NULL AND action = 'webhook')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_queue_backlog_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_queue_backlog_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('queue_oldest_age_seconds' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_queue_backlog_chk CHECK ((%s) OR metric IN (%L, %L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'queue_backlog', 'queue_oldest_age_seconds');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_queue_backlog_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_queue_backlog_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_queue_backlog_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_queue_backlog_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_queue_backlog_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE now_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('queue_backlog', 'queue_oldest_age_seconds')) THEN
  RAISE EXCEPTION 'delete queue_backlog and queue_oldest_age_seconds alert rules before rollback';
 END IF;
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_queue_backlog_chk;
 SELECT pg_get_constraintdef(oid) INTO now_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(' OR (metric = ANY (ARRAY[''queue_backlog''::text, ''queue_oldest_age_seconds''::text]))' in now_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(now_def, ' OR (metric = ANY (ARRAY[''queue_backlog''::text, ''queue_oldest_age_seconds''::text]))', '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
