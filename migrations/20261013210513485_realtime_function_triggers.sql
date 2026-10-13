-- +goose Up
-- ADR-1237: a kind=realtime trigger delivers a bound consumer group's
-- messages as invocations. A group binds to at most one trigger; deleting
-- the trigger unbinds the group and keeps its messages.
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
    CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue','realtime'));

ALTER TABLE managed_realtime_consumer_groups
    ADD COLUMN IF NOT EXISTS trigger_id uuid REFERENCES triggers(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS managed_realtime_consumer_groups_trigger
    ON managed_realtime_consumer_groups(trigger_id) WHERE trigger_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM triggers WHERE kind='realtime') THEN
  RAISE EXCEPTION 'delete realtime triggers before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX IF EXISTS managed_realtime_consumer_groups_trigger;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN IF EXISTS trigger_id;
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
    CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue'));
