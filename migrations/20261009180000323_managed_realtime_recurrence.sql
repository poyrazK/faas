-- +goose Up
ALTER TABLE managed_realtime_schedules
 ADD COLUMN IF NOT EXISTS interval_seconds integer NOT NULL DEFAULT 0 CHECK(interval_seconds=0 OR interval_seconds BETWEEN 5 AND 2592000),
 ADD COLUMN IF NOT EXISTS max_occurrences bigint NOT NULL DEFAULT 0 CHECK(max_occurrences BETWEEN 0 AND 1000000),
 ADD COLUMN IF NOT EXISTS end_at timestamptz,
 ADD COLUMN IF NOT EXISTS initial_deliver_at timestamptz,
 ADD COLUMN IF NOT EXISTS occurrence bigint NOT NULL DEFAULT 1 CHECK(occurrence BETWEEN 1 AND 9007199254740991),
 ADD COLUMN IF NOT EXISTS completed_occurrences bigint NOT NULL DEFAULT 0 CHECK(completed_occurrences BETWEEN 0 AND 9007199254740991);
UPDATE managed_realtime_schedules SET initial_deliver_at=deliver_at WHERE initial_deliver_at IS NULL;
ALTER TABLE managed_realtime_schedules ALTER COLUMN initial_deliver_at SET NOT NULL;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_realtime_schedules'::regclass AND conname='managed_realtime_schedules_status_check' AND pg_get_constraintdef(oid) LIKE '%''paused''%') THEN
  ALTER TABLE managed_realtime_schedules DROP CONSTRAINT IF EXISTS managed_realtime_schedules_status_check;
  ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedules_status_check CHECK(status IN ('pending','paused','published','canceled','failed'));
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_schedule_history ADD COLUMN IF NOT EXISTS occurrence bigint NOT NULL DEFAULT 1, ADD COLUMN IF NOT EXISTS completed_occurrences bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_realtime_schedule_history'::regclass AND conname='managed_realtime_schedule_history_event_check' AND pg_get_constraintdef(oid) LIKE '%''paused''%') THEN
  ALTER TABLE managed_realtime_schedule_history DROP CONSTRAINT IF EXISTS managed_realtime_schedule_history_event_check;
  ALTER TABLE managed_realtime_schedule_history ADD CONSTRAINT managed_realtime_schedule_history_event_check CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published','paused','resumed'));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Paused recurring series become canceled before removing recurrence support.
UPDATE managed_realtime_schedules SET status='canceled' WHERE status='paused';
ALTER TABLE managed_realtime_schedules DROP CONSTRAINT managed_realtime_schedules_status_check;
ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedules_status_check CHECK(status IN ('pending','published','canceled','failed'));
DELETE FROM managed_realtime_schedule_history WHERE event IN ('paused','resumed');
ALTER TABLE managed_realtime_schedule_history DROP CONSTRAINT managed_realtime_schedule_history_event_check;
ALTER TABLE managed_realtime_schedule_history ADD CONSTRAINT managed_realtime_schedule_history_event_check CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published'));
ALTER TABLE managed_realtime_schedule_history DROP COLUMN occurrence,DROP COLUMN completed_occurrences;
ALTER TABLE managed_realtime_schedules DROP COLUMN interval_seconds,DROP COLUMN max_occurrences,DROP COLUMN end_at,DROP COLUMN initial_deliver_at,DROP COLUMN occurrence,DROP COLUMN completed_occurrences;
