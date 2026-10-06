-- filename: 20261001160000001_runtime_config_restart_status_idx.sql

-- +goose Up
CREATE INDEX IF NOT EXISTS notification_outbox_runtime_config_status_idx
    ON notification_outbox (
        (payload::jsonb ->> 'app_id'),
        (payload::jsonb ->> 'wake_id'),
        id DESC
    )
    WHERE channel = 'runtime_config_restart';

-- +goose Down
DROP INDEX IF EXISTS notification_outbox_runtime_config_status_idx;
