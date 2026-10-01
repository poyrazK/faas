-- filename: 20261001081007501_invocation_queue_binding_identity.sql

-- +goose Up
-- Accepted queue work retains its binding identity across rename and retirement.
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS queue_binding_id uuid;
CREATE INDEX IF NOT EXISTS invocations_queue_binding_scope_idx
  ON invocations (app_id,queue_binding_id,deployment_scope,state,created_at)
  WHERE source='queue' AND state IN ('pending','dispatching','dead_letter');
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='invocations'::regclass
    AND conname='invocation_queue_binding_source') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocation_queue_binding_source
      CHECK (queue_binding_id IS NULL OR source='queue');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='invocations'::regclass
    AND conname='invocation_queue_binding_tenant') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocation_queue_binding_tenant
      FOREIGN KEY (queue_binding_id,app_id,account_id) REFERENCES queue_bindings(id,app_id,account_id)
      DEFERRABLE INITIALLY DEFERRED;
  END IF;
  -- Only run historical capture before installing the immutable ledger guard.
  -- Name alone cannot prove ownership after a historical rename. Durable private
  -- consumer receipts can; otherwise require the current name to predate admission.
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='invocations'::regclass
    AND tgname='invocation_queue_binding_guard') THEN
    WITH candidates AS (
      SELECT i.id, b.id AS binding_id FROM invocations i JOIN queue_bindings b
        ON b.app_id=i.app_id AND b.account_id=i.account_id AND b.queue_name=i.queue_name
        AND b.updated_at<=i.created_at
        WHERE i.source='queue' AND i.queue_binding_id IS NULL AND i.queue_name<>''
      UNION
      SELECT i.id, b.id FROM invocations i JOIN trigger_records r ON r.item_identifier=i.id::text
        JOIN triggers t ON t.id=r.trigger_id JOIN queue_bindings b ON b.id=t.queue_binding_id
        AND b.app_id=i.app_id AND b.account_id=i.account_id
        WHERE i.source='queue' AND i.queue_binding_id IS NULL
    ), proved AS (
      SELECT id, min(binding_id::text)::uuid AS binding_id FROM candidates
      GROUP BY id HAVING count(DISTINCT binding_id)=1
    ) UPDATE invocations i SET queue_binding_id=p.binding_id FROM proved p WHERE i.id=p.id;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_invocation_queue_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.queue_binding_id IS DISTINCT FROM OLD.queue_binding_id OR
      (OLD.queue_binding_id IS NOT NULL AND (NEW.app_id IS DISTINCT FROM OLD.app_id
        OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.queue_name IS DISTINCT FROM OLD.queue_name)) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_identity',
        MESSAGE='accepted queue binding identity is immutable';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.queue_binding_id IS NOT NULL AND NEW.source<>'queue' THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_source',
      MESSAGE='binding identity requires a queue invocation';
  END IF;
  IF NEW.source='queue' THEN
    IF NEW.queue_binding_id IS NULL AND NEW.queue_name<>'' THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.queue_name=NEW.queue_name;
    ELSIF NEW.queue_binding_id IS NULL AND NEW.work_policy_name IS NULL THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b
        JOIN triggers t ON t.queue_binding_id=b.id
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.enabled
          AND b.mode='push' AND b.retired_at IS NULL AND t.enabled AND t.kind='queue' AND t.source='queue'
          AND NOT EXISTS (SELECT 1 FROM triggers other WHERE other.app_id=NEW.app_id
            AND other.kind='queue' AND other.source='queue' AND other.enabled AND other.id<>t.id)
        ;
    END IF;
    -- Lock by the observed immutable ID. Rechecking the mutable name/enabled
    -- predicate after waiting would turn an owned message into legacy work.
    IF NEW.queue_binding_id IS NOT NULL THEN
      PERFORM 1 FROM queue_bindings b WHERE b.id=NEW.queue_binding_id
        AND b.app_id=NEW.app_id AND b.account_id=NEW.account_id FOR SHARE;
      IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_tenant',
          MESSAGE='queue binding must belong to the admitted app and account';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_retired_queue_invocation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE binding_retired_at timestamptz;
BEGIN
  IF NEW.source='queue' AND (TG_OP='INSERT' OR
    (NEW.state='dispatching' AND OLD.state IS DISTINCT FROM 'dispatching')) THEN
    SELECT b.retired_at INTO binding_retired_at FROM queue_bindings b
      WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND
        (b.id=NEW.queue_binding_id OR (NEW.queue_binding_id IS NULL AND b.queue_name=NEW.queue_name)) FOR SHARE;
    IF binding_retired_at IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retired',
        MESSAGE='queue binding is retired';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS invocation_queue_binding_guard ON invocations;
CREATE TRIGGER invocation_queue_binding_guard BEFORE INSERT OR UPDATE ON invocations
FOR EACH ROW EXECUTE FUNCTION guard_invocation_queue_binding();

-- +goose Down
-- Retained routing identity cannot be removed by rollback.
SELECT 1;
