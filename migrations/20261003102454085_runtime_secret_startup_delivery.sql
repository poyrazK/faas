-- +goose Up
-- +goose StatementBegin
-- adr:436 A successful exec can consume the current projection without a signal.
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_runtime_reload_observation_consistent;
ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_runtime_reload_observation_consistent CHECK (
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
                   last_runtime_reload_signal IN ('sent', 'not_attempted') AND
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
ALTER TABLE app_secret_runtime_reload_observations DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_observation_outcome_chk;
ALTER TABLE app_secret_runtime_reload_observations ADD CONSTRAINT app_secret_runtime_reload_observation_outcome_chk CHECK (
        (projection = 'failed' AND signal = 'not_attempted' AND error_code IS NOT DISTINCT FROM 'projection_failed') OR
        (projection = 'updated' AND signal IN ('sent', 'queued', 'not_attempted') AND error_code IS NULL) OR
        (projection = 'updated' AND signal = 'failed' AND error_code IS NOT DISTINCT FROM 'signal_failed') OR
        (projection = 'unchanged' AND signal = 'not_attempted' AND error_code IS NULL)
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Remove only observations that the older closed contract cannot represent.
DELETE FROM app_secret_runtime_reload_observations WHERE projection = 'updated' AND signal = 'not_attempted';
UPDATE app_secrets SET
 last_runtime_reload_version = NULL, last_runtime_reload_revision = NULL,
 last_runtime_reload_projection = NULL, last_runtime_reload_signal = NULL,
 last_runtime_reload_at = NULL, last_runtime_reload_error_code = NULL,
 last_runtime_reload_instance_id = NULL
 WHERE last_runtime_reload_projection = 'updated' AND last_runtime_reload_signal = 'not_attempted';
ALTER TABLE app_secrets DROP CONSTRAINT app_secrets_runtime_reload_observation_consistent;
ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_runtime_reload_observation_consistent CHECK (
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
ALTER TABLE app_secret_runtime_reload_observations DROP CONSTRAINT app_secret_runtime_reload_observation_outcome_chk;
ALTER TABLE app_secret_runtime_reload_observations ADD CONSTRAINT app_secret_runtime_reload_observation_outcome_chk CHECK (
        (projection = 'failed' AND signal = 'not_attempted' AND error_code IS NOT DISTINCT FROM 'projection_failed') OR
        (projection = 'updated' AND signal IN ('sent', 'queued') AND error_code IS NULL) OR
        (projection = 'updated' AND signal = 'failed' AND error_code IS NOT DISTINCT FROM 'signal_failed') OR
        (projection = 'unchanged' AND signal = 'not_attempted' AND error_code IS NULL)
    );
-- +goose StatementEnd
