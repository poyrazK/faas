-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_secret_runtime_reload_observations
    ADD COLUMN application_ack_version bigint,
    ADD COLUMN application_ack_status text,
    ADD COLUMN application_ack_at timestamptz,
    ADD COLUMN application_ack_error_code text,
    ADD CONSTRAINT app_secret_runtime_reload_application_ack_version_chk
        CHECK (application_ack_version IS NULL OR application_ack_version >= 1),
    ADD CONSTRAINT app_secret_runtime_reload_application_ack_status_chk
        CHECK (application_ack_status IS NULL OR application_ack_status IN ('applied', 'failed')),
    ADD CONSTRAINT app_secret_runtime_reload_application_ack_outcome_chk CHECK (
        (application_ack_version IS NULL AND application_ack_status IS NULL AND application_ack_at IS NULL AND application_ack_error_code IS NULL) OR
        (application_ack_version IS NOT NULL AND application_ack_status IS NOT NULL AND application_ack_at IS NOT NULL AND (
            (application_ack_status = 'applied' AND application_ack_error_code IS NULL) OR
            (application_ack_status = 'failed' AND application_ack_error_code IS NOT DISTINCT FROM 'application_reload_failed')
        ))
    );
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
