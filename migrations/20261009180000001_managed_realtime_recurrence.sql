-- +goose Up
ALTER TABLE managed_realtime_schedules
 ADD COLUMN interval_seconds integer NOT NULL DEFAULT 0 CHECK(interval_seconds=0 OR interval_seconds BETWEEN 5 AND 2592000),
 ADD COLUMN max_occurrences bigint NOT NULL DEFAULT 0 CHECK(max_occurrences BETWEEN 0 AND 1000000),
 ADD COLUMN end_at timestamptz,
 ADD COLUMN initial_deliver_at timestamptz,
 ADD COLUMN occurrence bigint NOT NULL DEFAULT 1 CHECK(occurrence BETWEEN 1 AND 9007199254740991),
 ADD COLUMN completed_occurrences bigint NOT NULL DEFAULT 0 CHECK(completed_occurrences BETWEEN 0 AND 9007199254740991);
UPDATE managed_realtime_schedules SET initial_deliver_at=deliver_at;
ALTER TABLE managed_realtime_schedules ALTER COLUMN initial_deliver_at SET NOT NULL;
ALTER TABLE managed_realtime_schedules DROP CONSTRAINT managed_realtime_schedules_status_check;
ALTER TABLE managed_realtime_schedules ADD CONSTRAINT managed_realtime_schedules_status_check CHECK(status IN ('pending','paused','published','canceled','failed'));
ALTER TABLE managed_realtime_schedule_history ADD COLUMN occurrence bigint NOT NULL DEFAULT 1, ADD COLUMN completed_occurrences bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_realtime_schedule_history DROP CONSTRAINT managed_realtime_schedule_history_event_check;
ALTER TABLE managed_realtime_schedule_history ADD CONSTRAINT managed_realtime_schedule_history_event_check CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published','paused','resumed'));
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
