-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS environment_scope text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_environment_scope_chk'
    ) THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_environment_scope_chk CHECK (
          environment_scope = '' OR (
            app_id IS NOT NULL
            AND comparison IN ('above_baseline', 'below_baseline')
            AND metric IN ('request_count', 'error_rate_pct', 'latency_p95_ms')
            AND (observe_only OR action = 'webhook')
            AND environment_scope <> 'pr-preview'
            AND environment_scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$'
          )
        ) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules DROP COLUMN environment_scope;
