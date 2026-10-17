-- +goose Up
-- Quiesce old schedd writers before applying. Pending rows can be reclaimed
-- legacy executions; no status alone proves whether their effect happened.
UPDATE operator_intents SET status='outcome_unknown', finished_at=now(), claim_token=NULL,
 error='Legacy node maintenance requires operator reconciliation before another action.'
WHERE kind IN ('node_drain','node_force_drain','node_activate','node_retire')
 AND status IN ('pending','running');

CREATE TABLE operator_node_executions (
 intent_id uuid PRIMARY KEY REFERENCES operator_intents(id) ON DELETE RESTRICT,
 claim_token uuid NOT NULL CHECK (claim_token <> '00000000-0000-0000-0000-000000000000'),
 kind text NOT NULL CHECK (kind IN ('node_drain','node_force_drain','node_activate','node_retire')),
 target_id text NOT NULL,
 previous_lifecycle text NOT NULL CHECK (previous_lifecycle IN ('','active','draining','force_draining','unavailable','recovering','maintenance','retired')),
 result_lifecycle text NOT NULL CHECK (result_lifecycle IN ('','active','draining','force_draining','unavailable','recovering','maintenance','retired')),
 changed boolean NOT NULL,
 status text NOT NULL CHECK (status IN ('succeeded','failed')),
 error text NOT NULL CHECK (octet_length(error) <= 1024),
 finished_at timestamptz NOT NULL,
 audit_outbox_id bigint NOT NULL CHECK (audit_outbox_id > 0),
 CHECK ((status='succeeded') = (error='')),
 CHECK (NOT changed OR (status='succeeded' AND previous_lifecycle<>'' AND previous_lifecycle<>result_lifecycle)),
 CHECK (changed OR previous_lifecycle=result_lifecycle)
);

-- +goose StatementBegin
CREATE FUNCTION operator_node_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Operator node execution receipts are immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER operator_node_receipt_immutable BEFORE UPDATE OR DELETE ON operator_node_executions
 FOR EACH ROW EXECUTE FUNCTION operator_node_receipt_immutable();

-- +goose StatementBegin
CREATE FUNCTION operator_node_completion_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt operator_node_executions%ROWTYPE;
BEGIN
 IF OLD.kind IN ('node_drain','node_force_drain','node_activate','node_retire') THEN
  SELECT * INTO receipt FROM operator_node_executions WHERE intent_id=OLD.id;
  IF FOUND THEN
   IF (OLD.status IN ('succeeded','failed') AND NEW IS DISTINCT FROM OLD)
    OR (NEW.kind,NEW.target_id,NEW.actor_id,NEW.account_id,NEW.reason,NEW.metadata,NEW.trace_id)
       IS DISTINCT FROM (OLD.kind,OLD.target_id,OLD.actor_id,OLD.account_id,OLD.reason,OLD.metadata,OLD.trace_id)
    OR (NEW.kind,NEW.target_id,NEW.claim_token,NEW.status,coalesce(NEW.error,''),NEW.finished_at,NEW.completion_audit_outbox_id)
       IS DISTINCT FROM (receipt.kind,receipt.target_id,receipt.claim_token,receipt.status,receipt.error,receipt.finished_at,receipt.audit_outbox_id) THEN
    RAISE EXCEPTION 'Operator node completion does not match immutable receipt' USING ERRCODE='23514';
   END IF;
  ELSIF NEW.status IN ('succeeded','failed') THEN
   RAISE EXCEPTION 'Operator node completion requires an atomic receipt' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER operator_node_completion_guard BEFORE UPDATE ON operator_intents
 FOR EACH ROW EXECUTE FUNCTION operator_node_completion_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM operator_node_executions)
  OR EXISTS (SELECT 1 FROM operator_intents WHERE kind IN ('node_drain','node_force_drain','node_activate','node_retire') AND status='outcome_unknown') THEN
  RAISE EXCEPTION 'Preserve operator node maintenance evidence before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER operator_node_completion_guard ON operator_intents;
DROP FUNCTION operator_node_completion_guard();
DROP TRIGGER operator_node_receipt_immutable ON operator_node_executions;
DROP FUNCTION operator_node_receipt_immutable();
DROP TABLE operator_node_executions;
