-- +goose Up
-- +goose StatementBegin
-- Persist guest-init's latest live-secret refresh outcome per secret version.
-- This is deliberately not an application acknowledgement: the guest can
-- confirm its local projection and signal syscall, but only the app can say
-- whether it parsed and applied the new credentials.

ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS last_runtime_reload_version BIGINT,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_revision TEXT,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_projection TEXT,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_signal TEXT,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_error_code TEXT,
    ADD COLUMN IF NOT EXISTS last_runtime_reload_instance_id TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'app_secrets_runtime_reload_observation_consistent'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_runtime_reload_observation_consistent
            CHECK (
                (last_runtime_reload_version IS NULL AND
                 last_runtime_reload_revision IS NULL AND
                 last_runtime_reload_projection IS NULL AND
                 last_runtime_reload_signal IS NULL AND
                 last_runtime_reload_at IS NULL AND
                 last_runtime_reload_error_code IS NULL AND
                 last_runtime_reload_instance_id IS NULL)
                OR
                (last_runtime_reload_version IS NOT NULL AND
                 last_runtime_reload_revision IS NOT NULL AND
                 last_runtime_reload_projection IS NOT NULL AND
                 last_runtime_reload_signal IS NOT NULL AND
                 last_runtime_reload_at IS NOT NULL AND
                 last_runtime_reload_instance_id IS NOT NULL AND
                 last_runtime_reload_version >= 1 AND
                 last_runtime_reload_version <= delivery_version AND
                 last_runtime_reload_revision ~ '^[a-f0-9]{64}$' AND
                 last_runtime_reload_projection IN ('updated', 'unchanged', 'failed') AND
                 last_runtime_reload_signal IN ('sent', 'queued', 'failed', 'not_attempted') AND
                 last_runtime_reload_at IS NOT NULL AND
                 last_runtime_reload_instance_id IS NOT NULL AND
                 ((last_runtime_reload_projection = 'failed' AND
                   last_runtime_reload_signal = 'not_attempted' AND
                   last_runtime_reload_error_code IS NOT NULL AND
                   last_runtime_reload_error_code = 'projection_failed') OR
                  (last_runtime_reload_projection = 'updated' AND
                   last_runtime_reload_signal = 'sent' AND
                   last_runtime_reload_error_code IS NULL) OR
                  (last_runtime_reload_projection = 'updated' AND
                   last_runtime_reload_signal = 'queued' AND
                   last_runtime_reload_error_code IS NULL) OR
                  (last_runtime_reload_projection = 'updated' AND
                   last_runtime_reload_signal = 'failed' AND
                   last_runtime_reload_error_code IS NOT NULL AND
                   last_runtime_reload_error_code = 'signal_failed') OR
                  (last_runtime_reload_projection = 'unchanged' AND
                   last_runtime_reload_signal = 'not_attempted' AND
                   last_runtime_reload_error_code IS NULL))
            ));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_secrets
    DROP CONSTRAINT IF EXISTS app_secrets_runtime_reload_observation_consistent,
    DROP COLUMN IF EXISTS last_runtime_reload_instance_id,
    DROP COLUMN IF EXISTS last_runtime_reload_error_code,
    DROP COLUMN IF EXISTS last_runtime_reload_at,
    DROP COLUMN IF EXISTS last_runtime_reload_signal,
    DROP COLUMN IF EXISTS last_runtime_reload_projection,
    DROP COLUMN IF EXISTS last_runtime_reload_revision,
    DROP COLUMN IF EXISTS last_runtime_reload_version;
-- +goose StatementEnd
