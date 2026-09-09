-- filename: 20260909120100000_request_telemetry_timeline.sql
-- +goose Up
-- +goose StatementBegin
--
-- Debugger v1.1: retain the causal identifiers that are already available at
-- the gateway request exit funnel.  A request row can now be joined to the
-- append-only wake event stream without retaining request bodies, headers, or
-- other customer payloads.
--
-- Both values are nullable for backwards compatibility: warm requests do not
-- have a newly admitted wake, and rolling upgrades leave older rows NULL.
ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS wake_id text,
    ADD COLUMN IF NOT EXISTS instance_id text;

-- The request-id lookup remains the primary path.  This index is for future
-- operational joins and bounded retention scans by app/wake.
CREATE INDEX IF NOT EXISTS request_telemetry_app_wake_received_idx
    ON request_telemetry (app_id, wake_id, received_at DESC)
    WHERE wake_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS request_telemetry_app_wake_received_idx;
ALTER TABLE request_telemetry
    DROP COLUMN IF EXISTS instance_id,
    DROP COLUMN IF EXISTS wake_id;
-- +goose StatementEnd
