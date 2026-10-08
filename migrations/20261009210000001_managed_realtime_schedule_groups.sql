-- +goose Up
ALTER TABLE managed_realtime_schedules ADD COLUMN schedule_group text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedule_group_chk
 CHECK (octet_length(schedule_group) <= 128 AND schedule_group = btrim(schedule_group) AND schedule_group !~ E'[\\r\\n]');
CREATE INDEX managed_realtime_schedule_group_idx ON managed_realtime_schedules(endpoint_id,channel,schedule_group,schedule_id);

-- +goose Down
DROP INDEX managed_realtime_schedule_group_idx;
ALTER TABLE managed_realtime_schedules DROP COLUMN schedule_group;
