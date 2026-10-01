-- filename: 20260930223007001_exclusive_broker_trigger_bindings.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE exclusive_work_trigger_bindings
  DROP CONSTRAINT exclusive_work_trigger_bindings_source_check,
  ADD CONSTRAINT exclusive_work_trigger_bindings_source_check
    CHECK (source IN ('cron', 'inbound_webhook', 'broker'));

ALTER TABLE trigger_dead_letter
  DROP CONSTRAINT trigger_dead_letter_reason_check,
  ADD CONSTRAINT trigger_dead_letter_reason_check
    CHECK (reason IN ('rate_limited', 'poison_record', 'max_attempts', 'broker_error',
                      'plan_quota', 'payload_too_large', 'customer_disabled', 'exclusive_operation_rejected'));

CREATE FUNCTION delete_exclusive_broker_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM exclusive_work_trigger_bindings WHERE source = 'broker' AND trigger_id = OLD.id;
  RETURN OLD;
END;
$$;
CREATE TRIGGER triggers_delete_exclusive_broker_binding
  AFTER DELETE ON triggers FOR EACH ROW EXECUTE FUNCTION delete_exclusive_broker_binding();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER triggers_delete_exclusive_broker_binding ON triggers;
DROP FUNCTION delete_exclusive_broker_binding();
DELETE FROM exclusive_work_trigger_bindings WHERE source = 'broker';
ALTER TABLE exclusive_work_trigger_bindings
  DROP CONSTRAINT exclusive_work_trigger_bindings_source_check,
  ADD CONSTRAINT exclusive_work_trigger_bindings_source_check
    CHECK (source IN ('cron', 'inbound_webhook'));
ALTER TABLE trigger_dead_letter
  DROP CONSTRAINT trigger_dead_letter_reason_check,
  ADD CONSTRAINT trigger_dead_letter_reason_check
    CHECK (reason IN ('rate_limited', 'poison_record', 'max_attempts', 'broker_error',
                      'plan_quota', 'payload_too_large', 'customer_disabled'));
-- +goose StatementEnd
