-- migration-compat: expand
-- migration-lint: allow index-build managed_realtime_consumer_groups: the partial index covers only trigger_id, added NULL on every row by this migration, so it starts empty; the table holds one row per consumer group
-- +goose Up
-- ADR-1322: a kind=realtime trigger delivers a bound consumer group's
-- messages as invocations. A group binds to at most one trigger; deleting
-- the trigger unbinds the group and keeps its messages. The previous release
-- never creates kind=realtime triggers and never reads trigger_id.
SET LOCAL lock_timeout = '5s';
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
    CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue','realtime'));

ALTER TABLE managed_realtime_consumer_groups
    ADD COLUMN IF NOT EXISTS trigger_id uuid REFERENCES triggers(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS managed_realtime_consumer_groups_trigger
    ON managed_realtime_consumer_groups(trigger_id) WHERE trigger_id IS NOT NULL;

-- Wake schedd's trigger tick when a message lands on a channel whose group a
-- realtime trigger consumes, instead of waiting for the next 1 s tick. The
-- notification is delivered at commit; identical payloads in one
-- transaction collapse, so a batch publish wakes each trigger once.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_realtime_trigger_ready() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bound uuid;
BEGIN
 FOR bound IN SELECT trigger_id FROM managed_realtime_consumer_groups
   WHERE endpoint_id=NEW.endpoint_id AND channel=NEW.channel AND trigger_id IS NOT NULL LOOP
  PERFORM pg_notify('trigger_ready', json_build_object('trigger_id', bound, 'source', 'realtime')::text);
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS managed_realtime_trigger_ready ON managed_realtime_channel_messages;
CREATE TRIGGER managed_realtime_trigger_ready AFTER INSERT ON managed_realtime_channel_messages
    FOR EACH ROW EXECUTE FUNCTION notify_realtime_trigger_ready();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM triggers WHERE kind='realtime') THEN
  RAISE EXCEPTION 'delete realtime triggers before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS managed_realtime_trigger_ready ON managed_realtime_channel_messages;
DROP FUNCTION IF EXISTS notify_realtime_trigger_ready();
DROP INDEX IF EXISTS managed_realtime_consumer_groups_trigger;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN IF EXISTS trigger_id;
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
    CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue'));
