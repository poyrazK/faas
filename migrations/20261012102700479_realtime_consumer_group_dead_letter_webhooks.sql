-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.dead_lettered''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.dead_lettered');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_consumer_group_dead_letter()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_consumer_group_dead_letter() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; v_endpoint uuid;
 v_channel text; v_group text; v_max_attempts integer; event_id uuid; occurred_at timestamptz;
BEGIN
 IF NEW.status <> 'dead' OR OLD.status = 'dead' THEN RETURN NEW; END IF;
 SELECT e.app_id,e.account_id,g.endpoint_id,g.channel,g.name,g.max_attempts,array_agg(h.id ORDER BY h.id)
 INTO v_app,v_account,v_endpoint,v_channel,v_group,v_max_attempts,recipients
 FROM managed_realtime_consumer_groups g
 JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id
 JOIN app_webhooks h ON h.app_id=e.app_id AND h.account_id=e.account_id
 WHERE g.id=NEW.group_id AND h.scope='app' AND h.enabled
 AND 'realtime.consumer_group.dead_lettered'=ANY(h.event_filter)
 GROUP BY e.app_id,e.account_id,g.endpoint_id,g.channel,g.name,g.max_attempts;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid(); occurred_at:=clock_timestamp();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,'realtime.consumer_group.dead_lettered',event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',v_endpoint,
 'group_id',NEW.group_id,'group',v_group,'channel',v_channel,'sequence',NEW.sequence::text,
 'attempts',NEW.attempts,'max_attempts',v_max_attempts,'failure_code',NEW.failure_code,
 'occurred_at',occurred_at),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_consumer_group_dead_letter') THEN
CREATE TRIGGER realtime_consumer_group_dead_letter AFTER UPDATE ON managed_realtime_consumer_group_messages
 FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_consumer_group_dead_letter();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_consumer_group_dead_letter ON managed_realtime_consumer_group_messages;
DROP FUNCTION enqueue_realtime_consumer_group_dead_letter();
-- Retain event vocabulary for queued deliveries.
