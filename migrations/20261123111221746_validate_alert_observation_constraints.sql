-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  VALIDATE CONSTRAINT alert_rules_observe_only_chk,
  VALIDATE CONSTRAINT alert_rules_observed_incidents_chk,
  VALIDATE CONSTRAINT alert_rules_environment_scope_chk,
  VALIDATE CONSTRAINT alert_rules_recovery_confirmation_chk;

-- +goose Down
-- The earlier migrations own the constraints and remove them on rollback.
SELECT 1;
