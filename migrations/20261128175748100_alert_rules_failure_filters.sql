-- filename: 20261128175748100_alert_rules_failure_filters.sql
-- migration-compat: expand
-- ADR-045 amendment 1: a failure alert can watch one job or one cron.
-- job_name narrows a job_failures rule to the account's job of that name;
-- cron_id narrows a failed_invocations rule with failure_source cron to one
-- cron of the rule's app. Both are nullable additions; the checks are added
-- NOT VALID and validated, which only scans for the (absent) values.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS job_name text;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS cron_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_job_name_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_job_name_chk CHECK (
    job_name IS NULL OR (metric = 'job_failures' AND length(job_name) BETWEEN 1 AND 128)) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_cron_id_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_cron_id_chk CHECK (
    cron_id IS NULL OR (metric = 'failed_invocations' AND failure_source = 'cron' AND app_id IS NOT NULL)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_job_name_chk;
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_cron_id_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_cron_id_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_job_name_chk;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS cron_id;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS job_name;
RESET lock_timeout;
