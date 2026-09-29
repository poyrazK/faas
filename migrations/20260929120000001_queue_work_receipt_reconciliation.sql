-- +goose Up
-- +goose StatementBegin
ALTER TABLE trigger_records DROP CONSTRAINT trigger_records_state_check;
ALTER TABLE trigger_records ADD CONSTRAINT trigger_records_state_check
    CHECK (state IN ('pending', 'claimed', 'succeeded', 'retry', 'dead_letter',
                    'superseded', 'cancelled', 'expired'));

-- The invocation is authoritative for pending work. A receipt left in retry
-- after a failed delivery must not continue to appear runnable when that
-- invocation is replaced, cancelled, or expired.
CREATE OR REPLACE FUNCTION reconcile_queue_work_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE trigger_records tr
       SET state = NEW.state,
           claim_expires_at = NULL,
           last_error = 'queue work ' || NEW.state
      FROM triggers t
     WHERE t.id = tr.trigger_id
       AND t.app_id = NEW.app_id
       AND t.kind = 'queue'
       AND tr.item_identifier = NEW.id::text
       AND tr.state IN ('pending', 'retry');
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS queue_work_receipt_reconciliation ON invocations;
CREATE TRIGGER queue_work_receipt_reconciliation
    AFTER UPDATE OF state ON invocations
    FOR EACH ROW
    WHEN (OLD.state = 'pending' AND NEW.source = 'queue'
          AND NEW.state IN ('superseded', 'cancelled', 'expired'))
    EXECUTE FUNCTION reconcile_queue_work_receipt();

-- Repair receipts created before this migration.
UPDATE trigger_records tr
   SET state = i.state,
       claim_expires_at = NULL,
       last_error = 'queue work ' || i.state
  FROM triggers t, invocations i
 WHERE t.id = tr.trigger_id
   AND t.kind = 'queue'
   AND t.app_id = i.app_id
   AND i.source = 'queue'
   AND tr.item_identifier = i.id::text
   AND i.state IN ('superseded', 'cancelled', 'expired')
   AND tr.state IN ('pending', 'retry');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS queue_work_receipt_reconciliation ON invocations;
DROP FUNCTION IF EXISTS reconcile_queue_work_receipt();
UPDATE trigger_records
   SET state = 'dead_letter',
       last_error = coalesce(last_error, 'queue work policy terminal')
 WHERE state IN ('superseded', 'cancelled', 'expired');
ALTER TABLE trigger_records DROP CONSTRAINT trigger_records_state_check;
ALTER TABLE trigger_records ADD CONSTRAINT trigger_records_state_check
    CHECK (state IN ('pending', 'claimed', 'succeeded', 'retry', 'dead_letter'));
-- +goose StatementEnd
