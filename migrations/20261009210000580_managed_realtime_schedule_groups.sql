-- +goose Up
ALTER TABLE managed_realtime_schedules ADD COLUMN IF NOT EXISTS schedule_group text NOT NULL DEFAULT '';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_realtime_schedules'::regclass AND conname='managed_realtime_schedule_group_chk') THEN
  ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedule_group_chk
 CHECK (octet_length(schedule_group) <= 128 AND schedule_group = btrim(schedule_group) AND schedule_group !~ E'[\\r\\n]');
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS managed_realtime_schedule_group_idx ON managed_realtime_schedules(endpoint_id,channel,schedule_group,schedule_id);

-- +goose Down
DROP INDEX managed_realtime_schedule_group_idx;
ALTER TABLE managed_realtime_schedules DROP COLUMN schedule_group;
