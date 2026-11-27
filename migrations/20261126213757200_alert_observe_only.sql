-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS observe_only boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS observation_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS observed_incidents jsonb;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_observe_only_chk'
    ) THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_observe_only_chk CHECK (
          (NOT observe_only AND NOT observation_enabled) OR
          (observe_only AND NOT enabled AND app_id IS NOT NULL AND comparison IN ('above_baseline', 'below_baseline'))
        ) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_observed_incidents_chk'
    ) THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_observed_incidents_chk CHECK (observed_incidents IS NULL OR jsonb_typeof(observed_incidents) = 'array') NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  DROP COLUMN observed_incidents,
  DROP COLUMN observation_enabled,
  DROP COLUMN observe_only;
