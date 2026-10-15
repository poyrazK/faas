-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_channel_webhook_filters (
 webhook_id uuid PRIMARY KEY REFERENCES app_webhooks(id) ON DELETE CASCADE,
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 channel text NOT NULL CHECK (octet_length(channel) BETWEEN 1 AND 256),
 metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata)='object')
);
CREATE INDEX IF NOT EXISTS managed_realtime_channel_webhook_filters_route ON managed_realtime_channel_webhook_filters(endpoint_id,channel);
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.channel.published''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.channel.published');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_channel_webhook()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_channel_webhook() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; event_id uuid;
BEGIN
 SELECT e.app_id,e.account_id,array_agg(h.id ORDER BY h.id)
 INTO v_app,v_account,recipients
 FROM managed_realtime_endpoints e
 JOIN managed_realtime_channel_webhook_filters f ON f.endpoint_id=e.id AND f.channel=NEW.channel
 JOIN app_webhooks h ON h.id=f.webhook_id AND h.app_id=e.app_id AND h.account_id=e.account_id
 WHERE e.id=NEW.endpoint_id AND h.scope='app' AND h.enabled
 AND 'realtime.channel.published'=ANY(h.event_filter)
 AND coalesce(NEW.metadata,'{}'::jsonb) @> f.metadata
 GROUP BY e.app_id,e.account_id;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,'realtime.channel.published',event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',NEW.endpoint_id,
 'channel',NEW.channel,'sequence',NEW.sequence::text,'data_base64',replace(encode(NEW.data,'base64'),E'\n',''),
 'binary',NEW.is_binary,'metadata',NEW.metadata,'target_message_id',NEW.target_message_id,
 'version',NEW.version,'message_event',NEW.message_event,'deleted',NEW.deleted,'occurred_at',NEW.created_at),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_channel_messages') AND tgname='managed_realtime_channel_webhook') THEN
CREATE TRIGGER managed_realtime_channel_webhook AFTER INSERT ON managed_realtime_channel_messages
 FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_channel_webhook();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER managed_realtime_channel_webhook ON managed_realtime_channel_messages;
DROP FUNCTION enqueue_realtime_channel_webhook();
DROP TABLE managed_realtime_channel_webhook_filters;
-- Retain event vocabulary so previously queued deliveries remain readable.
