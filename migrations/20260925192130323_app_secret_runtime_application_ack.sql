-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_secret_runtime_reload_observations
    ADD COLUMN IF NOT EXISTS application_ack_version bigint,
    ADD COLUMN IF NOT EXISTS application_ack_status text,
    ADD COLUMN IF NOT EXISTS application_ack_at timestamptz,
    ADD COLUMN IF NOT EXISTS application_ack_error_code text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secret_runtime_reload_application_ack_version_chk'
          AND conrelid = 'app_secret_runtime_reload_observations'::regclass
    ) THEN
        ALTER TABLE app_secret_runtime_reload_observations
            ADD CONSTRAINT app_secret_runtime_reload_application_ack_version_chk
                CHECK (application_ack_version IS NULL OR application_ack_version >= 1);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secret_runtime_reload_application_ack_status_chk'
          AND conrelid = 'app_secret_runtime_reload_observations'::regclass
    ) THEN
        ALTER TABLE app_secret_runtime_reload_observations
            ADD CONSTRAINT app_secret_runtime_reload_application_ack_status_chk
                CHECK (application_ack_status IS NULL OR application_ack_status IN ('applied', 'failed'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secret_runtime_reload_application_ack_outcome_chk'
          AND conrelid = 'app_secret_runtime_reload_observations'::regclass
    ) THEN
        ALTER TABLE app_secret_runtime_reload_observations
            ADD CONSTRAINT app_secret_runtime_reload_application_ack_outcome_chk CHECK (
        (application_ack_version IS NULL AND application_ack_status IS NULL AND application_ack_at IS NULL AND application_ack_error_code IS NULL) OR
        (application_ack_version IS NOT NULL AND application_ack_status IS NOT NULL AND application_ack_at IS NOT NULL AND (
            (application_ack_status = 'applied' AND application_ack_error_code IS NULL) OR
            (application_ack_status = 'failed' AND application_ack_error_code IS NOT DISTINCT FROM 'application_reload_failed')
        ))
            );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_secret_runtime_reload_observations
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_application_ack_outcome_chk,
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_application_ack_status_chk,
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_application_ack_version_chk,
    DROP COLUMN IF EXISTS application_ack_error_code,
    DROP COLUMN IF EXISTS application_ack_at,
    DROP COLUMN IF EXISTS application_ack_status,
    DROP COLUMN IF EXISTS application_ack_version;
-- +goose StatementEnd
