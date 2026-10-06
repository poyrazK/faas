-- +goose Up
ALTER TABLE invocations
 ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT ''
   CHECK (octet_length(outcome_code) <= 64);

ALTER TABLE schedule_occurrences
 ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT ''
   CHECK (octet_length(outcome_code) <= 64),
 ADD COLUMN IF NOT EXISTS work_decision jsonb;

-- A held receipt is a terminal invocation for automatic delivery purposes,
-- but its business result is explicitly uncertain.
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocations_outcome_check;
ALTER TABLE invocations ADD CONSTRAINT invocations_outcome_check
 CHECK (outcome IS NULL OR outcome IN ('success','failed','timeout','dead_letter','superseded','expired','uncertain'));

-- FailureRules are pinned execution policy: editing them advances the
-- revision so scheduler candidates created under the old policy are rejected.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revise_cron_schedule_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.schedule IS DISTINCT FROM OLD.schedule
    OR NEW.timezone IS DISTINCT FROM OLD.timezone
    OR NEW.schedule_policy IS DISTINCT FROM OLD.schedule_policy
    OR NEW.failure_rules IS DISTINCT FROM OLD.failure_rules THEN
  NEW.schedule_revision := OLD.schedule_revision + 1;
 END IF;
 RETURN NEW;
END;
$$;

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
     WHEN NEW.state = 'failed' AND NEW.work_decision->>'classification' = 'uncertain' THEN 'uncertain'
     WHEN COALESCE(missed_deadline, false) AND NEW.state = 'failed' THEN 'missed_deadline'
     WHEN NEW.state = 'pending' THEN CASE WHEN o.started_at IS NULL THEN 'queued' ELSE 'running' END
     WHEN NEW.state = 'dispatching' THEN 'running'
     WHEN NEW.state = 'completed' THEN 'succeeded'
     WHEN NEW.state = 'failed' THEN 'failed'
     WHEN NEW.state = 'cancelled' THEN 'cancelled'
     WHEN NEW.state = 'dead_letter' THEN 'failed'
     ELSE o.status END,
   reason = CASE
     WHEN NEW.state = 'failed' AND NEW.work_decision->>'classification' = 'uncertain'
       THEN COALESCE(NEW.work_decision->>'reason', 'completion_unknown')
     WHEN COALESCE(missed_deadline, false) AND NEW.state = 'failed'
       THEN 'invocation did not start before the occurrence start deadline'
     ELSE o.reason END,
   outcome_code = COALESCE(NEW.outcome_code, o.outcome_code),
   work_decision = COALESCE(NEW.work_decision, o.work_decision),
   started_at = CASE WHEN NEW.state = 'dispatching' THEN COALESCE(o.started_at, clock_timestamp()) ELSE o.started_at END,
   finished_at = CASE WHEN NEW.state IN ('completed','failed','cancelled','dead_letter') THEN COALESCE(NEW.completed_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.id = NEW.occurrence_id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: classified occurrence history and uncertain outcomes must survive rollback.
SELECT 1;
