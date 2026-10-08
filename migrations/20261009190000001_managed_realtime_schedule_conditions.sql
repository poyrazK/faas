-- +goose Up
ALTER TABLE managed_realtime_schedules
 ADD COLUMN IF NOT EXISTS conditions json CHECK(conditions IS NULL OR (json_typeof(conditions)='array' AND octet_length(conditions::text)<=4096)),
 ADD COLUMN IF NOT EXISTS on_condition_failure text NOT NULL DEFAULT '' CHECK(on_condition_failure IN ('','skip','retry')),
 ADD COLUMN IF NOT EXISTS skipped_occurrences bigint NOT NULL DEFAULT 0 CHECK(skipped_occurrences BETWEEN 0 AND 9007199254740991),
 ADD COLUMN IF NOT EXISTS skip_reason text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_schedules DROP CONSTRAINT managed_realtime_schedules_status_check;
ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedules_status_check CHECK(status IN ('pending','paused','published','canceled','failed','skipped'));
ALTER TABLE managed_realtime_schedule_history ADD COLUMN IF NOT EXISTS skipped_occurrences bigint NOT NULL DEFAULT 0, ADD COLUMN IF NOT EXISTS skip_reason text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_schedule_history DROP CONSTRAINT managed_realtime_schedule_history_event_check;
ALTER TABLE managed_realtime_schedule_history ADD CONSTRAINT managed_realtime_schedule_history_event_check CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published','paused','resumed','skipped'));
-- +goose Down
UPDATE managed_realtime_schedules SET status='canceled' WHERE status='skipped';
ALTER TABLE managed_realtime_schedules DROP CONSTRAINT managed_realtime_schedules_status_check;
ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedules_status_check CHECK(status IN ('pending','paused','published','canceled','failed'));
DELETE FROM managed_realtime_schedule_history WHERE event='skipped';
ALTER TABLE managed_realtime_schedule_history DROP CONSTRAINT managed_realtime_schedule_history_event_check;
ALTER TABLE managed_realtime_schedule_history ADD CONSTRAINT managed_realtime_schedule_history_event_check CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published','paused','resumed'));
ALTER TABLE managed_realtime_schedule_history DROP COLUMN skipped_occurrences,DROP COLUMN skip_reason;
ALTER TABLE managed_realtime_schedules DROP COLUMN conditions,DROP COLUMN on_condition_failure,DROP COLUMN skipped_occurrences,DROP COLUMN skip_reason;
