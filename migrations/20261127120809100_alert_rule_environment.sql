-- filename: 20261127120809100_alert_rule_environment.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000 amendment 2026-11-22: an alert rule belongs to one
-- environment of its app's project. NULL is production, which every existing
-- rule keeps. The environment's deletion removes its rules.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS environment_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_environment_fkey') THEN
    ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_environment_fkey
      FOREIGN KEY (environment_id) REFERENCES project_environments(id) ON DELETE CASCADE NOT VALID;
  END IF;
  -- A stage rule runs only on a signal the stage has of its own: request
  -- metrics counted per environment host, and failed invocations, compared
  -- with a fixed threshold and answered by a webhook. Any other stage rule
  -- (a clone copies every rule) stays paused.
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_environment_enabled_chk') THEN
    ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_environment_enabled_chk CHECK (
      environment_id IS NULL OR NOT enabled OR (
        metric IN ('request_count','error_rate_pct','latency_p50_ms','latency_p95_ms','latency_p99_ms','cold_start_pct','failed_invocations')
        AND comparison IN ('gt','gte','lt','lte')
        AND action = 'webhook')) NOT VALID;
  END IF;
END $$;

-- A rule's environment is one of its app's project environments and never
-- changes; an account-wide rule is production's.
CREATE OR REPLACE FUNCTION guard_alert_rule_environment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.environment_id IS DISTINCT FROM OLD.environment_id THEN
    RAISE EXCEPTION 'an alert rule keeps its environment' USING ERRCODE = '23514';
  END IF;
  IF NEW.environment_id IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM apps a JOIN project_environments e ON e.project_id = a.project_id AND e.account_id = a.account_id
      WHERE a.id = NEW.app_id AND a.account_id = NEW.account_id AND e.id = NEW.environment_id) THEN
    RAISE EXCEPTION 'an alert rule environment belongs to its app''s project' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS alert_rule_environment_guard ON alert_rules;
CREATE TRIGGER alert_rule_environment_guard BEFORE INSERT OR UPDATE OF environment_id, app_id, account_id ON alert_rules
  FOR EACH ROW EXECUTE FUNCTION guard_alert_rule_environment();
-- alert_rules_environment_idx is built concurrently by
-- 20261127120809200_alert_rule_environment_index.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS alert_rule_environment_guard ON alert_rules;
DROP FUNCTION IF EXISTS guard_alert_rule_environment();
DELETE FROM alert_rules WHERE environment_id IS NOT NULL;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_environment_enabled_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_environment_fkey;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS environment_id;
-- +goose StatementEnd
