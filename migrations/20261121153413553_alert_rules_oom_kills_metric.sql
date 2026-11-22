-- filename: 20261121153413553_alert_rules_oom_kills_metric.sql
-- migration-compat: expand
-- ADR-20261121190000 (Observe audit 2026-11-21, finding 18): oom_kills counts
-- the app's instances killed for running out of memory within the window, the
-- kills `gregale debug status` lists. apid requires an app for it.
--
-- The metric CHECK is replaced online, as in
-- 20261112122644583_ai_budget_alert_metric: a widened copy of its current
-- definition (which keeps metrics other migrations added) is added NOT VALID
-- under a new name and validated, the old one is dropped, the old name is
-- re-added from the copy NOT VALID and validated, and the copy is dropped.
-- Every step commits on its own and is guarded, so writers never wait on a
-- scan and a rerun after a partial failure converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_oom_kills_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('''oom_kills''' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_oom_kills_chk CHECK ((%s) OR metric IN (%L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'oom_kills');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_oom_kills_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_oom_kills_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_oom_kills_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_oom_kills_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_oom_kills_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'oom_kills') THEN
  RAISE EXCEPTION 'delete oom_kills alert rules before rollback';
 END IF;
 SELECT pg_get_constraintdef(oid) INTO rules_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(' OR (metric = ''oom_kills''::text)' in rules_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(rules_def, ' OR (metric = ''oom_kills''::text)', '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
