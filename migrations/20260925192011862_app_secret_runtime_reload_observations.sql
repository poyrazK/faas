-- +goose Up
-- +goose StatementBegin
-- Keep guest-init refresh outcomes per runtime instead of allowing the most
-- recent runtime to overwrite every other runtime's status on app_secrets.
CREATE TABLE IF NOT EXISTS app_secret_runtime_reload_observations (
    app_id uuid NOT NULL,
    scope text NOT NULL,
    key text NOT NULL,
    instance_id uuid NOT NULL,
    secret_version bigint NOT NULL,
    projection text NOT NULL,
    signal text NOT NULL,
    observed_at timestamptz NOT NULL,
    error_code text,
    PRIMARY KEY (app_id, scope, key, instance_id),
    CONSTRAINT app_secret_runtime_reload_observation_secret_fkey
        FOREIGN KEY (app_id, scope, key)
        REFERENCES app_secrets (app_id, scope, key) ON DELETE CASCADE,
    CONSTRAINT app_secret_runtime_reload_observation_instance_fkey
        FOREIGN KEY (instance_id) REFERENCES instances (id) ON DELETE CASCADE,
    CONSTRAINT app_secret_runtime_reload_observation_version_chk CHECK (secret_version >= 1),
    CONSTRAINT app_secret_runtime_reload_observation_projection_chk
        CHECK (projection IN ('updated', 'unchanged', 'failed')),
    CONSTRAINT app_secret_runtime_reload_observation_signal_chk
        CHECK (signal IN ('sent', 'queued', 'failed', 'not_attempted')),
    CONSTRAINT app_secret_runtime_reload_observation_outcome_chk CHECK (
        (projection = 'failed' AND signal = 'not_attempted' AND error_code IS NOT DISTINCT FROM 'projection_failed') OR
        (projection = 'updated' AND signal IN ('sent', 'queued') AND error_code IS NULL) OR
        (projection = 'updated' AND signal = 'failed' AND error_code IS NOT DISTINCT FROM 'signal_failed') OR
        (projection = 'unchanged' AND signal = 'not_attempted' AND error_code IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS app_secret_runtime_reload_observations_instance_idx
    ON app_secret_runtime_reload_observations (instance_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_secret_runtime_reload_observations;
-- +goose StatementEnd
