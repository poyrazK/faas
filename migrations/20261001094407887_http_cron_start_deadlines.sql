-- filename: 20261001094407887_http_cron_start_deadlines.sql

-- +goose Up
-- Invocation-backed schedule occurrences keep their execution history while
-- retries return the invocation to pending. An occurrence becomes missed
-- only when it terminalizes before its first dispatch and its deadline passed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_invocation_schedule_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 missed_deadline boolean;
BEGIN
 IF NEW.state IS NOT DISTINCT FROM OLD.state THEN RETURN NEW; END IF;
 SELECT o.started_at IS NULL AND NEW.start_deadline_at IS NOT NULL
        AND NEW.start_deadline_at < COALESCE(NEW.completed_at, clock_timestamp())
   INTO missed_deadline
   FROM schedule_occurrences o WHERE o.id = NEW.occurrence_id;
 UPDATE schedule_occurrences o SET
   status = CASE
     WHEN COALESCE(missed_deadline, false) AND NEW.state = 'failed' THEN 'missed_deadline'
     WHEN NEW.state = 'pending' THEN CASE WHEN o.started_at IS NULL THEN 'queued' ELSE 'running' END
     WHEN NEW.state = 'dispatching' THEN 'running'
     WHEN NEW.state = 'completed' THEN 'succeeded'
     WHEN NEW.state = 'failed' THEN 'failed'
     WHEN NEW.state = 'cancelled' THEN 'cancelled'
     WHEN NEW.state = 'dead_letter' THEN 'failed'
     ELSE o.status END,
   reason = CASE WHEN COALESCE(missed_deadline, false) AND NEW.state = 'failed'
       THEN 'invocation did not start before the occurrence start deadline' ELSE o.reason END,
   started_at = CASE WHEN NEW.state = 'dispatching' THEN COALESCE(o.started_at, clock_timestamp()) ELSE o.started_at END,
   finished_at = CASE WHEN NEW.state IN ('completed','failed','cancelled','dead_letter') THEN COALESCE(NEW.completed_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.id = NEW.occurrence_id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_invocation_schedule_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IS NOT DISTINCT FROM OLD.state THEN RETURN NEW; END IF;
 UPDATE schedule_occurrences o SET
   status = CASE NEW.state
     WHEN 'pending' THEN 'queued' WHEN 'dispatching' THEN 'running'
     WHEN 'completed' THEN 'succeeded' WHEN 'failed' THEN 'failed'
     WHEN 'cancelled' THEN 'cancelled' WHEN 'dead_letter' THEN 'failed' ELSE o.status END,
   reason = CASE WHEN o.started_at IS NULL AND NEW.start_deadline_at IS NOT NULL
       AND NEW.start_deadline_at < COALESCE(NEW.completed_at, clock_timestamp())
       AND NEW.state = 'failed' THEN 'invocation did not start before the occurrence start deadline' ELSE o.reason END,
   started_at = CASE WHEN NEW.state = 'dispatching' THEN COALESCE(o.started_at, clock_timestamp()) ELSE o.started_at END,
   finished_at = CASE WHEN NEW.state IN ('completed','failed','cancelled','dead_letter') THEN COALESCE(NEW.completed_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.id = NEW.occurrence_id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
