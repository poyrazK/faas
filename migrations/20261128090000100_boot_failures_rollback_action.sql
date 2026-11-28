-- filename: 20261128090000100_boot_failures_rollback_action.sql
-- migration-compat: expand
-- ADR-605 amendment 2: boot_failures rules may roll back or demote. A
-- completed release rolls back only on its own boot evidence (failed boots
-- meet the rule, none completed since cutover), so a node fault under a
-- healthy release never rolls it back. job_failures stays webhook-only and
-- boot_failures never promotes. alert_rules_failure_actions_chk is added NOT
-- VALID and validated, then the webhook-only check it replaces is dropped.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_failure_actions_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_failure_actions_chk CHECK (
    ((metric <> 'job_failures') OR (action = 'webhook'))
    AND ((metric <> 'boot_failures') OR (action <> 'promote'))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_failure_actions_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_failure_count_action_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'boot_failures' AND action <> 'webhook') THEN
  RAISE EXCEPTION 'set boot_failures alert rules to the webhook action before rollback';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_failure_count_action_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_failure_count_action_chk
    CHECK ((metric NOT IN ('boot_failures', 'job_failures')) OR (action = 'webhook'));
 END IF;
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_failure_actions_chk;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
