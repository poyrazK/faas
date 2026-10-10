-- +goose Up
ALTER TABLE managed_realtime_schedules
 ADD COLUMN IF NOT EXISTS max_attempts integer NOT NULL DEFAULT 1 CHECK(max_attempts BETWEEN 1 AND 10),
 ADD COLUMN IF NOT EXISTS backoff_seconds integer NOT NULL DEFAULT 5 CHECK(backoff_seconds BETWEEN 5 AND 3600),
 ADD COLUMN IF NOT EXISTS attempts bigint NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 9007199254740991),
 ADD COLUMN IF NOT EXISTS cycle_attempts integer NOT NULL DEFAULT 0 CHECK(cycle_attempts BETWEEN 0 AND 10),
 ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz,
 ADD COLUMN IF NOT EXISTS last_attempt_at timestamptz;
UPDATE managed_realtime_schedules SET attempts=1,cycle_attempts=1,last_attempt_at=updated_at WHERE status IN ('published','failed') AND attempts=0;
DROP INDEX managed_realtime_schedule_due_idx;
CREATE INDEX IF NOT EXISTS managed_realtime_schedule_due_idx ON managed_realtime_schedules(coalesce(next_attempt_at,deliver_at),endpoint_id,channel,schedule_id) WHERE status='pending';
-- +goose Down
DROP INDEX managed_realtime_schedule_due_idx;
CREATE INDEX managed_realtime_schedule_due_idx ON managed_realtime_schedules(deliver_at,endpoint_id,channel,schedule_id) WHERE status='pending';
ALTER TABLE managed_realtime_schedules DROP COLUMN max_attempts,DROP COLUMN backoff_seconds,DROP COLUMN attempts,DROP COLUMN cycle_attempts,DROP COLUMN next_attempt_at,DROP COLUMN last_attempt_at;
