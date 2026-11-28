-- filename: 20261128090000100_boot_failures_rollback_action.sql
-- migration-compat: expand
-- ADR-605 amendment 2: a boot_failures rule may roll back a completed
-- release inside its post-deploy rollback window, judged only on that
-- release's own boots (failed boots on otherwise healthy nodes meet the rule,
-- none completed since cutover), so a node or fleet fault never rolls a
-- healthy release back. It never acts on an in-flight canary (no demote, no
-- promote) and its rollback needs a window. job_failures stays webhook-only. alert_rules_failure_actions_chk is added NOT
-- VALID and validated, then the webhook-only check it replaces is dropped.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_failure_actions_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_failure_actions_chk CHECK (
    ((metric <> 'job_failures') OR (action = 'webhook'))
    AND ((metric <> 'boot_failures') OR (action = 'webhook')
         OR (action = 'rollback' AND post_deploy_rollback_window_seconds > 0))) NOT VALID;
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
