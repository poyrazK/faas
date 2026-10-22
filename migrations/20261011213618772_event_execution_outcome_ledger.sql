-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_execution_coverage ADD COLUMN IF NOT EXISTS ledger_started_at timestamptz;
ALTER TABLE event_execution_coverage ADD COLUMN IF NOT EXISTS ledger_baseline_known boolean NOT NULL DEFAULT false;
ALTER TABLE event_execution_coverage ADD COLUMN IF NOT EXISTS ledger_lost_through_at timestamptz;
UPDATE event_execution_coverage SET ledger_started_at=clock_timestamp() WHERE ledger_started_at IS NULL;
CREATE TABLE IF NOT EXISTS event_execution_roots (
 root_invocation_id uuid NOT NULL, root_created_at timestamptz NOT NULL,
 subscription_id uuid NOT NULL REFERENCES event_execution_coverage(subscription_id) ON DELETE CASCADE,
 account_id uuid NOT NULL, app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 accepted_at timestamptz NOT NULL, outbox_id bigint NOT NULL CHECK (outbox_id>0),
 retention_seconds integer NOT NULL CHECK (retention_seconds>0),
 PRIMARY KEY(root_invocation_id,root_created_at), CHECK (isfinite(root_created_at) AND isfinite(accepted_at))
);
CREATE INDEX IF NOT EXISTS event_execution_roots_owner_idx ON event_execution_roots(account_id,app_id,subscription_id,root_created_at);
CREATE INDEX IF NOT EXISTS event_execution_roots_retention_idx ON event_execution_roots(root_created_at,root_invocation_id);
CREATE TABLE IF NOT EXISTS event_execution_outcomes (
 root_invocation_id uuid NOT NULL, root_created_at timestamptz NOT NULL,
 invocation_id uuid NOT NULL, replay_generation bigint NOT NULL CHECK (replay_generation>=0),
 attempt integer NOT NULL CHECK (attempt>=0), kind text NOT NULL CHECK (kind IN ('attempt','completion')),
 subscription_id uuid NOT NULL, account_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 finished_at timestamptz NOT NULL, outcome text NOT NULL CHECK (outcome IN ('succeeded','retry','failed','dead_letter','cancelled','unknown')),
 latency_seconds double precision NOT NULL CHECK (latency_seconds>=0 AND latency_seconds<'Infinity'::double precision),
 retain_until timestamptz NOT NULL,
 invocation_created_at timestamptz NOT NULL CHECK (isfinite(invocation_created_at)),
 PRIMARY KEY(invocation_id,invocation_created_at,replay_generation,attempt,kind),
 FOREIGN KEY(root_invocation_id,root_created_at) REFERENCES event_execution_roots(root_invocation_id,root_created_at) ON DELETE CASCADE,
 CHECK (finished_at>=root_created_at AND retain_until>finished_at),
 CHECK ((kind='attempt' AND attempt>0) OR (kind='completion' AND outcome='succeeded' AND attempt=0))
);
CREATE INDEX IF NOT EXISTS event_execution_outcomes_window_idx ON event_execution_outcomes(account_id,app_id,subscription_id,finished_at);
CREATE INDEX IF NOT EXISTS event_execution_outcomes_retention_idx ON event_execution_outcomes(retain_until,invocation_id);
CREATE OR REPLACE FUNCTION start_event_execution_coverage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE started timestamptz := clock_timestamp();
BEGIN
 INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known,ledger_started_at,ledger_baseline_known)
 VALUES(NEW.id,NEW.account_id,NEW.app_id,started,true,started,true) ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION record_event_execution_outcome(
 p_id uuid,p_created timestamptz,p_root uuid,p_root_created timestamptz,p_account uuid,p_app uuid,
 p_generation bigint,p_attempt integer,p_kind text,p_finished timestamptz,p_outcome text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE root_record event_execution_roots%ROWTYPE;
BEGIN
 SELECT * INTO root_record FROM event_execution_roots WHERE root_invocation_id=coalesce(p_root,p_id)
 AND root_created_at=coalesce(p_root_created,p_created) AND account_id=p_account AND app_id=p_app;
 IF NOT FOUND THEN RETURN; END IF;
 IF EXISTS (SELECT 1 FROM event_execution_outcomes WHERE invocation_id=p_id AND invocation_created_at=p_created AND replay_generation=p_generation AND attempt=p_attempt AND kind=p_kind) THEN RETURN; END IF;
 IF NOT isfinite(p_finished) OR p_finished<greatest(root_record.root_created_at,root_record.accepted_at) THEN
  UPDATE event_execution_coverage SET ledger_lost_through_at=greatest(ledger_lost_through_at,clock_timestamp(),root_record.root_created_at,root_record.accepted_at) WHERE subscription_id=root_record.subscription_id;
  RETURN;
 END IF;
 INSERT INTO event_execution_outcomes(root_invocation_id,root_created_at,invocation_id,invocation_created_at,replay_generation,attempt,kind,
 subscription_id,account_id,app_id,finished_at,outcome,latency_seconds,retain_until)
 SELECT r.root_invocation_id,r.root_created_at,p_id,p_created,p_generation,p_attempt,p_kind,
 r.subscription_id,r.account_id,r.app_id,p_finished,p_outcome,
 CASE WHEN p_kind='completion' THEN extract(epoch FROM p_finished-r.accepted_at)::double precision ELSE 0 END,
 p_finished+make_interval(secs=>r.retention_seconds)
 FROM event_execution_roots r JOIN apps a ON a.id=r.app_id
 WHERE r.root_invocation_id=coalesce(p_root,p_id) AND r.root_created_at=coalesce(p_root_created,p_created)
 AND r.account_id=p_account AND r.app_id=p_app AND p_finished>=r.root_created_at
 ON CONFLICT DO NOTHING;
END $$;
CREATE OR REPLACE FUNCTION capture_event_execution_outcome() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE finished timestamptz := clock_timestamp(); result text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.state='dispatching' AND OLD.attempts>0 THEN
   PERFORM record_event_execution_outcome(OLD.id,OLD.created_at,OLD.replay_root_invocation_id,OLD.replay_root_created_at,
    OLD.account_id,OLD.app_id,OLD.replay_generation,OLD.attempts,'attempt',finished,'unknown');
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.state='dispatching' AND OLD.attempts>0 AND (NEW.state<>'dispatching' OR NEW.attempts<>OLD.attempts OR NEW.replay_generation<>OLD.replay_generation) THEN
  result := CASE WHEN NEW.replay_generation<>OLD.replay_generation THEN 'unknown'
   WHEN NEW.state='completed' THEN 'succeeded' WHEN NEW.outcome='uncertain' THEN 'unknown'
   WHEN NEW.state='pending' AND NEW.last_error='dispatch lease expired; requeued' THEN 'unknown'
   WHEN NEW.state='pending' THEN 'retry' WHEN NEW.state='failed' THEN 'failed'
   WHEN NEW.state='dead_letter' THEN 'dead_letter' WHEN NEW.state='cancelled' THEN 'cancelled' ELSE 'unknown' END;
  PERFORM record_event_execution_outcome(OLD.id,OLD.created_at,OLD.replay_root_invocation_id,OLD.replay_root_created_at,
   OLD.account_id,OLD.app_id,OLD.replay_generation,OLD.attempts,'attempt',finished,result);
 END IF;
 IF NEW.state='completed' AND NEW.completed_at IS NOT NULL THEN
  PERFORM record_event_execution_outcome(NEW.id,NEW.created_at,NEW.replay_root_invocation_id,NEW.replay_root_created_at,
   NEW.account_id,NEW.app_id,NEW.replay_generation,0,'completion',NEW.completed_at,'succeeded');
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS event_execution_outcome_capture ON invocations;
CREATE TRIGGER event_execution_outcome_capture AFTER UPDATE OR DELETE ON invocations
 FOR EACH ROW EXECUTE FUNCTION capture_event_execution_outcome();
CREATE OR REPLACE FUNCTION reject_event_execution_outcome_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'event execution outcomes are immutable'; END $$;
DROP TRIGGER IF EXISTS event_execution_outcome_immutable ON event_execution_outcomes;
CREATE TRIGGER event_execution_outcome_immutable BEFORE UPDATE ON event_execution_outcomes
 FOR EACH ROW EXECUTE FUNCTION reject_event_execution_outcome_update();
CREATE OR REPLACE FUNCTION mark_event_execution_outcome_loss() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE event_execution_coverage SET ledger_lost_through_at=greatest(ledger_lost_through_at,OLD.finished_at)
 WHERE subscription_id=OLD.subscription_id;
 RETURN OLD;
END $$;
DROP TRIGGER IF EXISTS event_execution_outcome_loss ON event_execution_outcomes;
CREATE TRIGGER event_execution_outcome_loss BEFORE DELETE ON event_execution_outcomes
 FOR EACH ROW EXECUTE FUNCTION mark_event_execution_outcome_loss();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS event_execution_outcome_loss ON event_execution_outcomes;
DROP TRIGGER IF EXISTS event_execution_outcome_immutable ON event_execution_outcomes;
DROP FUNCTION reject_event_execution_outcome_update();
DROP TRIGGER IF EXISTS event_execution_outcome_capture ON invocations;
DROP FUNCTION mark_event_execution_outcome_loss();
DROP FUNCTION capture_event_execution_outcome();
DROP FUNCTION record_event_execution_outcome(uuid,timestamptz,uuid,timestamptz,uuid,uuid,bigint,integer,text,timestamptz,text);
DROP TABLE event_execution_outcomes;
DROP TABLE event_execution_roots;
CREATE OR REPLACE FUNCTION start_event_execution_coverage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known)
 VALUES(NEW.id,NEW.account_id,NEW.app_id,clock_timestamp(),true) ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
ALTER TABLE event_execution_coverage DROP COLUMN ledger_lost_through_at;
ALTER TABLE event_execution_coverage DROP COLUMN ledger_baseline_known;
ALTER TABLE event_execution_coverage DROP COLUMN ledger_started_at;
-- +goose StatementEnd
