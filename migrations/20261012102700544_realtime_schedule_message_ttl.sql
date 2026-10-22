-- +goose Up
ALTER TABLE managed_realtime_schedules ADD COLUMN IF NOT EXISTS ttl_seconds integer NOT NULL DEFAULT 0 CHECK (ttl_seconds BETWEEN 0 AND 86400);
-- +goose Down
ALTER TABLE managed_realtime_schedules DROP COLUMN ttl_seconds;
