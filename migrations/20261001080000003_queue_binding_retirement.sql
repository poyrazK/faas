-- filename: 20261001080000003_queue_binding_retirement.sql

-- +goose Up
-- Queue pruning retains durable consumer and delivery identity (ADR-387).
-- Existing bindings remain active; no legacy scope or ownership is inferred.
ALTER TABLE queue_bindings ADD COLUMN IF NOT EXISTS retired_at timestamptz;
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='queue_bindings'::regclass
    AND conname='queue_bindings_retired_disabled') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_retired_disabled
      CHECK (retired_at IS NULL OR NOT enabled);
  END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_queue_binding_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.retired_at IS NOT NULL AND EXISTS (SELECT 1 FROM apps WHERE id=OLD.app_id) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retirement_identity',
        MESSAGE='retired queue identity requires explicit recovery';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.retired_at IS NOT NULL AND
    (NEW.retired_at IS DISTINCT FROM OLD.retired_at
      OR NEW.id IS DISTINCT FROM OLD.id
      OR NEW.account_id IS DISTINCT FROM OLD.account_id
      OR NEW.app_id IS DISTINCT FROM OLD.app_id
      OR NEW.name IS DISTINCT FROM OLD.name
      OR NEW.queue_name IS DISTINCT FROM OLD.queue_name
      OR NEW.mode IS DISTINCT FROM OLD.mode
      OR NEW.workload_class IS DISTINCT FROM OLD.workload_class
      OR NEW.enabled IS DISTINCT FROM OLD.enabled
      OR NEW.max_concurrency IS DISTINCT FROM OLD.max_concurrency
      OR NEW.retry_policy IS DISTINCT FROM OLD.retry_policy) THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retirement_identity',
      MESSAGE='retired queue binding requires explicit recovery';
  END IF;
  RETURN NEW;
END;
$$;

-- Lock the name's binding for all queue admissions and new dispatches.
-- Retirement serializes on that same row, so a cached poller or generic drain
-- cannot bypass the hold. Existing deliveries may still finish; retries and
-- dead-letter replay retain their rows and wait for explicit recovery.
CREATE OR REPLACE FUNCTION guard_retired_queue_invocation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE binding_retired_at timestamptz;
BEGIN
  IF NEW.source='queue' AND (TG_OP='INSERT' OR
    (NEW.state='dispatching' AND OLD.state IS DISTINCT FROM 'dispatching')) THEN
    SELECT b.retired_at INTO binding_retired_at FROM queue_bindings b
      WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND
        (b.id=(to_jsonb(NEW)->>'queue_binding_id')::uuid OR
          (to_jsonb(NEW)->>'queue_binding_id' IS NULL AND b.queue_name=NEW.queue_name)) FOR SHARE;
    IF binding_retired_at IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retired',
        MESSAGE='queue binding is retired';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

-- Receipts also survive push-to-pull and removal. A stale dispatcher cannot
-- acquire a new receipt generation after the binding is held. Returning NULL
-- skips that UPDATE without changing the receipt or its attempt counters.
CREATE OR REPLACE FUNCTION guard_held_queue_consumer_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE can_claim boolean;
BEGIN
  IF NEW.state='claimed' AND (OLD.state IS DISTINCT FROM 'claimed'
    OR NEW.claim_generation IS DISTINCT FROM OLD.claim_generation) THEN
    SELECT b.enabled AND b.mode='push' AND b.retired_at IS NULL INTO can_claim
      FROM queue_bindings b JOIN triggers t ON t.queue_binding_id=b.id
      WHERE t.id=NEW.trigger_id FOR SHARE OF b;
    IF can_claim IS FALSE THEN RETURN NULL; END IF;
  END IF;
  RETURN NEW;
END;
$$;

-- A private consumer is a durable delivery namespace. Older write paths may
-- not delete it independently of its app and cascade away receipt history.
CREATE OR REPLACE FUNCTION guard_durable_queue_consumer_deletion() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.queue_binding_id IS NOT NULL AND EXISTS (SELECT 1 FROM apps WHERE id=OLD.app_id) THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_consumer_durable_identity',
      MESSAGE='private queue consumer identity requires explicit recovery';
  END IF;
  RETURN OLD;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS queue_binding_retirement_guard ON queue_bindings;
CREATE TRIGGER queue_binding_retirement_guard BEFORE UPDATE OR DELETE ON queue_bindings
FOR EACH ROW EXECUTE FUNCTION guard_queue_binding_retirement();
DROP TRIGGER IF EXISTS invocation_retired_queue_guard ON invocations;
CREATE TRIGGER invocation_retired_queue_guard BEFORE INSERT OR UPDATE OF state ON invocations
FOR EACH ROW EXECUTE FUNCTION guard_retired_queue_invocation();

DROP TRIGGER IF EXISTS trigger_record_held_queue_guard ON trigger_records;
CREATE TRIGGER trigger_record_held_queue_guard BEFORE UPDATE OF state, claim_generation ON trigger_records
FOR EACH ROW EXECUTE FUNCTION guard_held_queue_consumer_receipt();

DROP TRIGGER IF EXISTS trigger_durable_queue_consumer_guard ON triggers;
CREATE TRIGGER trigger_durable_queue_consumer_guard BEFORE DELETE ON triggers
FOR EACH ROW EXECUTE FUNCTION guard_durable_queue_consumer_deletion();

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
