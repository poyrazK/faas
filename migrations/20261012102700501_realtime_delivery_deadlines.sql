-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS deadline_metadata_key text NOT NULL DEFAULT '' CHECK (octet_length(deadline_metadata_key)<=64 AND deadline_metadata_key ~ '^[a-z0-9_.-]*$');
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS expired_deliveries bigint NOT NULL DEFAULT 0 CHECK (expired_deliveries>=0);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_groups') AND conname='realtime_group_deadline_ordering') THEN
ALTER TABLE managed_realtime_consumer_groups ADD CONSTRAINT realtime_group_deadline_ordering CHECK (deadline_metadata_key='' OR ordering_metadata_key='' OR dead_letter_policy='skip');
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS delivery_deadline timestamptz CHECK (delivery_deadline IS NULL OR isfinite(delivery_deadline));
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS deadline_invalid boolean NOT NULL DEFAULT false CHECK (NOT deadline_invalid OR delivery_deadline IS NULL);
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.delivery_expired''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.delivery_expired');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_delivery_expired()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_delivery_expired() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; v_endpoint uuid; v_channel text; v_group text; event_id uuid;
BEGIN
 IF NEW.status<>'dead' OR OLD.status='dead' OR NOT coalesce(NEW.deadline_invalid OR (NEW.delivery_deadline<=clock_timestamp() AND NEW.failure_code='delivery_expired'),false) THEN RETURN NEW; END IF;
 UPDATE managed_realtime_consumer_groups SET expired_deliveries=expired_deliveries+1 WHERE id=NEW.group_id;
 SELECT e.app_id,e.account_id,g.endpoint_id,g.channel,g.name,array_agg(h.id ORDER BY h.id)
 INTO v_app,v_account,v_endpoint,v_channel,v_group,recipients
 FROM managed_realtime_consumer_groups g JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id
 JOIN app_webhooks h ON h.account_id=e.account_id AND h.app_id=e.app_id
 WHERE g.id=NEW.group_id AND h.scope='app' AND h.enabled AND 'realtime.consumer_group.delivery_expired'=ANY(h.event_filter)
 GROUP BY e.app_id,e.account_id,g.endpoint_id,g.channel,g.name;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,'realtime.consumer_group.delivery_expired',event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',v_endpoint,'group_id',NEW.group_id,'channel',v_channel,'group',v_group,
 'sequence',NEW.sequence::text,'attempts',NEW.attempts,'failure_code',NEW.failure_code,'delivery_deadline',NEW.delivery_deadline,'occurred_at',clock_timestamp()),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_delivery_expired') THEN
CREATE TRIGGER realtime_delivery_expired AFTER UPDATE ON managed_realtime_consumer_group_messages FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_delivery_expired();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_delivery_expired ON managed_realtime_consumer_group_messages;
DROP FUNCTION enqueue_realtime_delivery_expired();
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN delivery_deadline,DROP COLUMN deadline_invalid;
ALTER TABLE managed_realtime_consumer_groups DROP CONSTRAINT realtime_group_deadline_ordering,DROP COLUMN deadline_metadata_key,DROP COLUMN expired_deliveries;
-- Queued expiry event vocabulary and existing dead snapshots remain.
