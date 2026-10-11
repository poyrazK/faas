-- +goose Up
-- +goose StatementBegin
-- Item-owned metadata deliberately has no invocation or attempt FK.
CREATE TABLE IF NOT EXISTS event_recovery_execution_results (
 job_id uuid NOT NULL,
 position bigint NOT NULL CHECK (position>0),
 replay_invocation_id uuid NOT NULL,
 replay_generation bigint NOT NULL CHECK (replay_generation>=0),
 replay_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('succeeded','failed','dead_lettered','expired','cancelled','superseded')),
 attempts integer NOT NULL CHECK (attempts>=0),
 completed_at timestamptz,
 recorded_at timestamptz NOT NULL,
 evidence_source text NOT NULL CHECK (evidence_source IN ('invocation','attempt_history')),
 PRIMARY KEY (job_id,position),
 FOREIGN KEY (job_id,position) REFERENCES event_recovery_items(job_id,position) ON DELETE CASCADE,
 CHECK (recorded_at>=replay_created_at),
 CHECK (completed_at IS NULL OR completed_at BETWEEN replay_created_at AND recorded_at)
);
CREATE INDEX IF NOT EXISTS event_recovery_items_execution_identity_idx
 ON event_recovery_items(replay_invocation_id,replay_generation,replay_created_at)
 WHERE state='queued' AND replay_invocation_id IS NOT NULL;

CREATE OR REPLACE FUNCTION capture_event_recovery_invocation_result(
 p_invocation uuid,p_account uuid,p_app uuid,p_created timestamptz,p_generation bigint,
 p_state text,p_outcome text,p_attempts integer,p_completed timestamptz)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
 observed timestamptz := clock_timestamp();
 terminal text := CASE p_state WHEN 'completed' THEN 'succeeded' WHEN 'failed' THEN 'failed'
  WHEN 'dead_letter' THEN 'dead_lettered' WHEN 'expired' THEN 'expired'
  WHEN 'cancelled' THEN 'cancelled' WHEN 'superseded' THEN 'superseded' END;
BEGIN
 IF terminal IS NULL OR p_outcome='uncertain' OR p_created>observed
  OR p_completed>observed OR p_completed<p_created THEN RETURN; END IF;
 INSERT INTO event_recovery_execution_results(job_id,position,replay_invocation_id,replay_generation,replay_created_at,
  state,attempts,completed_at,recorded_at,evidence_source)
 SELECT item.job_id,item.position,p_invocation,p_generation,p_created,terminal,p_attempts,p_completed,observed,'invocation'
 FROM event_recovery_items item JOIN event_recovery_jobs job ON job.id=item.job_id
 WHERE item.state='queued' AND job.selection->>'mode'='execution' AND job.account_id=p_account AND job.app_id=p_app
  AND item.replay_invocation_id=p_invocation AND item.replay_generation=p_generation AND item.replay_created_at=p_created
 ORDER BY item.job_id,item.position
 FOR KEY SHARE OF item
 ON CONFLICT (job_id,position) DO NOTHING;
END $$;

CREATE OR REPLACE FUNCTION record_event_recovery_invocation_result() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM capture_event_recovery_invocation_result(OLD.id,OLD.account_id,OLD.app_id,OLD.created_at,OLD.replay_generation,
   OLD.state,OLD.outcome,OLD.attempts,OLD.completed_at);
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (OLD.id,OLD.account_id,OLD.app_id,OLD.created_at,OLD.replay_generation)
   IS DISTINCT FROM (NEW.id,NEW.account_id,NEW.app_id,NEW.created_at,NEW.replay_generation) THEN
   PERFORM capture_event_recovery_invocation_result(OLD.id,OLD.account_id,OLD.app_id,OLD.created_at,OLD.replay_generation,
    OLD.state,OLD.outcome,OLD.attempts,OLD.completed_at);
  END IF;
 END IF;
 PERFORM capture_event_recovery_invocation_result(NEW.id,NEW.account_id,NEW.app_id,NEW.created_at,NEW.replay_generation,
  NEW.state,NEW.outcome,NEW.attempts,NEW.completed_at);
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS event_recovery_invocation_result ON invocations;
CREATE TRIGGER event_recovery_invocation_result
 AFTER INSERT OR DELETE OR UPDATE OF state,attempts,replay_generation,completed_at,outcome ON invocations
 FOR EACH ROW EXECUTE FUNCTION record_event_recovery_invocation_result();

CREATE OR REPLACE FUNCTION record_event_recovery_admitted_result() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state<>'queued' OR NEW.replay_invocation_id IS NULL THEN RETURN NEW; END IF;
 PERFORM capture_event_recovery_invocation_result(inv.id,inv.account_id,inv.app_id,inv.created_at,inv.replay_generation,
  inv.state,inv.outcome,inv.attempts,inv.completed_at)
 FROM invocations inv JOIN event_recovery_jobs job ON job.id=NEW.job_id
 WHERE job.selection->>'mode'='execution' AND inv.id=NEW.replay_invocation_id AND inv.account_id=job.account_id
  AND inv.app_id=job.app_id AND inv.replay_generation=NEW.replay_generation AND inv.created_at=NEW.replay_created_at;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS event_recovery_admitted_result ON event_recovery_items;
CREATE TRIGGER event_recovery_admitted_result
 AFTER INSERT OR UPDATE OF state,replay_invocation_id,replay_generation,replay_created_at ON event_recovery_items
 FOR EACH ROW EXECUTE FUNCTION record_event_recovery_admitted_result();

-- Backfill only exact tracked identities with still-available evidence.
SELECT capture_event_recovery_invocation_result(retained.id,retained.account_id,retained.app_id,retained.created_at,
 retained.replay_generation,retained.state,retained.outcome,retained.attempts,retained.completed_at)
FROM (
 SELECT DISTINCT inv.id,inv.account_id,inv.app_id,inv.created_at,inv.replay_generation,inv.state,inv.outcome,inv.attempts,inv.completed_at
 FROM event_recovery_items item JOIN event_recovery_jobs job ON job.id=item.job_id
 JOIN invocations inv ON inv.id=item.replay_invocation_id AND inv.account_id=job.account_id AND inv.app_id=job.app_id
  AND inv.replay_generation=item.replay_generation AND inv.created_at=item.replay_created_at
 WHERE job.selection->>'mode'='execution' AND item.state='queued'
  AND inv.state IN ('completed','failed','dead_letter','expired','cancelled','superseded')
) retained;

-- A latest retry/running/unknown attempt cannot be replaced by an older success.
WITH observation AS MATERIALIZED (SELECT clock_timestamp() AS now_at)
INSERT INTO event_recovery_execution_results(job_id,position,replay_invocation_id,replay_generation,replay_created_at,
 state,attempts,completed_at,recorded_at,evidence_source)
SELECT item.job_id,item.position,item.replay_invocation_id,item.replay_generation,item.replay_created_at,
 CASE history.outcome WHEN 'dead_letter' THEN 'dead_lettered' ELSE history.outcome END,
 history.attempt,history.finished_at,observation.now_at,'attempt_history'
FROM event_recovery_items item JOIN event_recovery_jobs job ON job.id=item.job_id CROSS JOIN observation
JOIN LATERAL (
 SELECT h.* FROM invocation_attempt_history h
 WHERE h.invocation_id=item.replay_invocation_id AND h.replay_generation=item.replay_generation
  AND h.account_id=job.account_id AND h.app_id=job.app_id
  AND h.started_at>=item.replay_created_at AND h.started_at<=observation.now_at AND h.retain_until>observation.now_at
 ORDER BY h.attempt DESC LIMIT 1
) history ON true
WHERE item.state='queued' AND job.selection->>'mode'='execution' AND item.replay_invocation_id IS NOT NULL
 AND history.outcome IN ('succeeded','failed','dead_letter','cancelled') AND history.finished_at<=observation.now_at
 -- The retained owner proves this incarnation, not just a reused UUID/generation.
 AND EXISTS (SELECT 1 FROM invocations owner WHERE owner.id=item.replay_invocation_id AND owner.account_id=job.account_id
  AND owner.app_id=job.app_id AND owner.created_at=item.replay_created_at)
 AND NOT EXISTS (SELECT 1 FROM invocations inv WHERE inv.id=item.replay_invocation_id AND inv.account_id=job.account_id
  AND inv.app_id=job.app_id AND inv.replay_generation=item.replay_generation AND inv.created_at=item.replay_created_at)
ORDER BY item.job_id,item.position
FOR KEY SHARE OF item
ON CONFLICT (job_id,position) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS event_recovery_admitted_result ON event_recovery_items;
DROP TRIGGER IF EXISTS event_recovery_invocation_result ON invocations;
DROP FUNCTION IF EXISTS record_event_recovery_admitted_result();
DROP FUNCTION IF EXISTS record_event_recovery_invocation_result();
DROP FUNCTION IF EXISTS capture_event_recovery_invocation_result(uuid,uuid,uuid,timestamptz,bigint,text,text,integer,timestamptz);
DROP INDEX IF EXISTS event_recovery_items_execution_identity_idx;
DROP TABLE IF EXISTS event_recovery_execution_results;
-- +goose StatementEnd
