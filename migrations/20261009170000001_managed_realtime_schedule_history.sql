-- +goose Up
CREATE TABLE managed_realtime_schedule_history (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 schedule_id text NOT NULL,
 version bigint NOT NULL CHECK(version>=1),
 event text NOT NULL CHECK(event IN ('baseline','created','rescheduled','canceled','manual_retry','attempt_failed','published')),
 status text NOT NULL,
 attempts bigint NOT NULL,
 cycle_attempts integer NOT NULL,
 deliver_at timestamptz NOT NULL,
 next_attempt_at timestamptz,
 failure_code text NOT NULL DEFAULT '',
 sequence bigint NOT NULL DEFAULT 0,
 occurred_at timestamptz NOT NULL,
 PRIMARY KEY(endpoint_id,channel,schedule_id,version),
 FOREIGN KEY(endpoint_id,channel,schedule_id) REFERENCES managed_realtime_schedules(endpoint_id,channel,schedule_id) ON DELETE CASCADE
);
INSERT INTO managed_realtime_schedule_history(endpoint_id,channel,schedule_id,version,event,status,attempts,cycle_attempts,deliver_at,next_attempt_at,failure_code,sequence,occurred_at)
 SELECT endpoint_id,channel,schedule_id,version,'baseline',status,attempts,cycle_attempts,deliver_at,next_attempt_at,last_error,sequence,updated_at FROM managed_realtime_schedules;
-- +goose Down
DROP TABLE managed_realtime_schedule_history;
