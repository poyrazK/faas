-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS recovery_confirmation_checks integer NOT NULL DEFAULT 1;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_recovery_confirmation_chk'
    ) THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_recovery_confirmation_chk CHECK (
          recovery_confirmation_checks = 1 OR (
            recovery_confirmation_checks = 2 AND app_id IS NOT NULL
            AND comparison IN ('above_baseline', 'below_baseline')
          )
        ) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules DROP COLUMN recovery_confirmation_checks;
