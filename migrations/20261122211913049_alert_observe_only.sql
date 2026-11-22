-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  ADD COLUMN observe_only boolean NOT NULL DEFAULT false,
  ADD COLUMN observation_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN observed_incidents jsonb,
  ADD CONSTRAINT alert_rules_observe_only_chk CHECK (
    (NOT observe_only AND NOT observation_enabled) OR
    (observe_only AND NOT enabled AND app_id IS NOT NULL AND comparison IN ('above_baseline', 'below_baseline'))
  ) NOT VALID,
  ADD CONSTRAINT alert_rules_observed_incidents_chk CHECK (observed_incidents IS NULL OR jsonb_typeof(observed_incidents) = 'array') NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  DROP COLUMN observed_incidents,
  DROP COLUMN observation_enabled,
  DROP COLUMN observe_only;
