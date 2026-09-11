-- filename: 20260911174237249_request_telemetry_consumer_id.sql

-- +goose Up
-- +goose StatementBegin

-- ADR-120: attribute gateway usage to the stable API consumer identity.
-- Nullable preserves anonymous traffic and rolling-upgrade compatibility;
-- credential rotation does not change this value.
ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS consumer_id uuid;

CREATE INDEX IF NOT EXISTS request_telemetry_app_consumer_received_idx
    ON request_telemetry (app_id, consumer_id, received_at DESC)
    WHERE consumer_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS request_telemetry_app_consumer_received_idx;
ALTER TABLE request_telemetry
    DROP COLUMN IF EXISTS consumer_id;
-- +goose StatementEnd
