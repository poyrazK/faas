-- filename: 20261001094704872_queue_replay_delivery_fence.sql

-- +goose Up
-- Retry budgets may restart, but delivery identity never does.
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS replay_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE trigger_dead_letter ADD COLUMN IF NOT EXISTS failure_history jsonb NOT NULL DEFAULT '[]';
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='invocations'::regclass
    AND conname='invocations_replay_generation_check') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocations_replay_generation_check CHECK (replay_generation>=0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='trigger_dead_letter'::regclass
    AND conname='trigger_dead_letter_failure_history_array') THEN
    ALTER TABLE trigger_dead_letter ADD CONSTRAINT trigger_dead_letter_failure_history_array
      CHECK (jsonb_typeof(failure_history)='array');
  END IF;
  -- Fence envelopes created before the upgrade for already replayed work.
  -- Ledger replay must never overwrite a current delivery generation.
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='invocations'::regclass
    AND tgname='invocation_replay_generation_guard') THEN
    UPDATE invocations SET replay_generation=1 WHERE last_replayed_at IS NOT NULL AND replay_generation=0;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_invocation_replay_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    NEW.replay_generation=0;
  ELSIF OLD.state='dead_letter' AND NEW.state='pending' THEN
    NEW.replay_generation=OLD.replay_generation+1;
  ELSIF NEW.replay_generation IS DISTINCT FROM OLD.replay_generation THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_replay_generation_identity',
      MESSAGE='invocation replay generation is owned by the delivery ledger';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION rearm_queue_replay_receipts() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.state='dead_letter' AND NEW.state='pending' AND NEW.source IN ('queue','delayed_task') THEN
    -- Preserve the original receipt UUID, payload, work lane and failure audit.
    -- A renamed private binding is addressed only by its immutable identity.
    UPDATE trigger_records r SET state='pending', attempts=0, last_error=NULL,
      next_fire_at=clock_timestamp(), claim_generation=r.claim_generation+1, claim_expires_at=NULL
      FROM triggers t WHERE t.id=r.trigger_id AND t.app_id=NEW.app_id AND t.account_id=NEW.account_id
        AND t.kind='queue' AND t.source=NEW.source AND r.item_identifier=NEW.id::text
        AND (NEW.queue_binding_id IS NULL OR t.queue_binding_id=NEW.queue_binding_id)
        AND r.state NOT IN ('superseded','cancelled','expired');
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION retain_queue_dead_letter_failures() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM triggers t WHERE t.id=NEW.trigger_id AND t.kind='queue'
    AND t.source IN ('queue','delayed_task')) THEN
    UPDATE trigger_dead_letter d SET reason=NEW.reason,routed_to=NEW.routed_to,
      detail=NEW.detail,created_at=NEW.created_at,
      failure_history=d.failure_history || jsonb_build_array(jsonb_build_object(
        'reason',d.reason,'routed_to',d.routed_to,'detail',d.detail,'created_at',d.created_at))
      WHERE d.record_id=NEW.record_id AND d.trigger_id=NEW.trigger_id;
    IF FOUND THEN RETURN NULL; END IF;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS invocation_replay_generation_guard ON invocations;
CREATE TRIGGER invocation_replay_generation_guard BEFORE INSERT OR UPDATE ON invocations
  FOR EACH ROW EXECUTE FUNCTION guard_invocation_replay_generation();
DROP TRIGGER IF EXISTS queue_replay_receipt_rearm ON invocations;
CREATE TRIGGER queue_replay_receipt_rearm AFTER UPDATE OF state ON invocations
  FOR EACH ROW EXECUTE FUNCTION rearm_queue_replay_receipts();
DROP TRIGGER IF EXISTS queue_dead_letter_failure_history ON trigger_dead_letter;
CREATE TRIGGER queue_dead_letter_failure_history BEFORE INSERT ON trigger_dead_letter
  FOR EACH ROW EXECUTE FUNCTION retain_queue_dead_letter_failures();
DROP TRIGGER IF EXISTS queue_dead_letter_recapture_event ON trigger_dead_letter;
CREATE TRIGGER queue_dead_letter_recapture_event AFTER UPDATE ON trigger_dead_letter
  FOR EACH ROW EXECUTE FUNCTION faas_capture_trigger_dead_letter_event();

-- +goose Down
-- Accepted delivery fences and retained receipts survive a rollback.
SELECT 1;
