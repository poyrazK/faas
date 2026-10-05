-- filename: 20261005101735681_invocation_attempt_history.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-600: dispatch evidence for async invocations and trusted replays.
CREATE TABLE IF NOT EXISTS invocation_attempt_history (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  invocation_id uuid NOT NULL REFERENCES invocations(id) ON DELETE CASCADE,
  account_id uuid NOT NULL, app_id uuid NOT NULL,
  root_invocation_id uuid NOT NULL, root_created_at timestamptz NOT NULL,
  replay_generation bigint NOT NULL CHECK (replay_generation>=0),
  attempt integer NOT NULL CHECK (attempt>0),
  started_at timestamptz NOT NULL, finished_at timestamptz,
  outcome text NOT NULL CHECK (outcome IN ('running','succeeded','retry','failed','dead_letter','cancelled','unknown')),
  error_detail text NOT NULL DEFAULT '', next_attempt_at timestamptz,
  retain_until timestamptz NOT NULL,
  UNIQUE (invocation_id,replay_generation,attempt),
  CHECK ((outcome='running')=(finished_at IS NULL)),
  CHECK (finished_at IS NULL OR finished_at>=started_at)
);
CREATE INDEX IF NOT EXISTS invocation_attempt_history_root_idx
  ON invocation_attempt_history(account_id,app_id,root_invocation_id,id DESC);
CREATE INDEX IF NOT EXISTS invocation_attempt_history_retention_idx
  ON invocation_attempt_history(retain_until,id) WHERE outcome<>'running';
CREATE OR REPLACE FUNCTION record_invocation_attempt_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  recorded_at timestamptz := clock_timestamp();
  attempt_outcome text;
BEGIN
  IF NEW.source NOT IN ('async_invoke','replay') THEN RETURN NEW; END IF;
  -- Both writes share the execution transaction. No backfill or pre-claim attempts.
  IF OLD.state='dispatching' AND (NEW.state<>'dispatching'
      OR NEW.attempts<>OLD.attempts OR NEW.replay_generation<>OLD.replay_generation) THEN
    attempt_outcome := CASE
      WHEN NEW.replay_generation<>OLD.replay_generation THEN 'unknown'
      WHEN NEW.state='completed' THEN 'succeeded'
      WHEN NEW.outcome='uncertain' THEN 'unknown'
      WHEN NEW.state='pending' AND NEW.last_error='dispatch lease expired; requeued' THEN 'unknown'
      WHEN NEW.state='pending' THEN 'retry'
      WHEN NEW.state='failed' THEN 'failed'
      WHEN NEW.state='dead_letter' THEN 'dead_letter'
      WHEN NEW.state='cancelled' THEN 'cancelled'
      ELSE 'unknown' END;
    UPDATE invocation_attempt_history SET
      finished_at=greatest(recorded_at,started_at), outcome=attempt_outcome,
      error_detail=left(coalesce(NEW.last_error,''),1024),
      next_attempt_at=CASE WHEN NEW.state='pending' THEN NEW.due_at END,
      retain_until=least(coalesce(NEW.result_retention_until,recorded_at+interval '30 days'),recorded_at+interval '30 days')
    WHERE invocation_id=OLD.id AND replay_generation=OLD.replay_generation
      AND attempt=OLD.attempts AND outcome='running';
  END IF;
  IF NEW.state='dispatching' AND NEW.attempts>0 AND (OLD.state<>'dispatching'
      OR NEW.attempts<>OLD.attempts OR NEW.replay_generation<>OLD.replay_generation) THEN
    INSERT INTO invocation_attempt_history(invocation_id,account_id,app_id,
      root_invocation_id,root_created_at,replay_generation,attempt,started_at,outcome,retain_until)
    VALUES(NEW.id,NEW.account_id,NEW.app_id,coalesce(NEW.replay_root_invocation_id,NEW.id),
      coalesce(NEW.replay_root_created_at,NEW.created_at),NEW.replay_generation,NEW.attempts,
      recorded_at,'running',recorded_at+interval '30 days');
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS invocation_attempt_history_transition ON invocations;
CREATE TRIGGER invocation_attempt_history_transition
  AFTER UPDATE OF state,attempts,replay_generation ON invocations
  FOR EACH ROW EXECUTE FUNCTION record_invocation_attempt_history();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER invocation_attempt_history_transition ON invocations;
DROP FUNCTION record_invocation_attempt_history();
DROP TABLE invocation_attempt_history;
-- +goose StatementEnd
