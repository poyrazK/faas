-- +goose Up
ALTER TABLE managed_realtime_durable_cursors ADD COLUMN IF NOT EXISTS acknowledged_sequence bigint CHECK (acknowledged_sequence > 0);
ALTER TABLE managed_realtime_durable_cursors ADD COLUMN IF NOT EXISTS acknowledged_at timestamptz;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_durable_cursors') AND conname='realtime_ack_pair') THEN
ALTER TABLE managed_realtime_durable_cursors ADD CONSTRAINT realtime_ack_pair CHECK ((acknowledged_sequence IS NULL) = (acknowledged_at IS NULL));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- Existing checkpoints are deliberately not backfilled: they may be baselines.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.channel.acknowledged''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.channel.acknowledged');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_channel_acknowledgement()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_channel_acknowledgement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; event_id uuid;
BEGIN
 IF NEW.acknowledged_sequence IS NULL OR NEW.acknowledged_sequence IS NOT DISTINCT FROM OLD.acknowledged_sequence THEN RETURN NEW; END IF;
 SELECT e.app_id,e.account_id,array_agg(h.id ORDER BY h.id) INTO v_app,v_account,recipients
 FROM managed_realtime_endpoints e JOIN app_webhooks h ON h.app_id=e.app_id AND h.account_id=e.account_id
 WHERE e.id=NEW.endpoint_id AND h.scope='app' AND h.enabled
 AND 'realtime.channel.acknowledged'=ANY(h.event_filter) GROUP BY e.app_id,e.account_id;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,'realtime.channel.acknowledged',event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',NEW.endpoint_id,
 'principal',NEW.principal,'subscription',NEW.subscription,'channel',NEW.channel,
 'sequence',NEW.acknowledged_sequence::text,'occurred_at',NEW.acknowledged_at),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_durable_cursors') AND tgname='realtime_channel_acknowledgement') THEN
CREATE TRIGGER realtime_channel_acknowledgement AFTER UPDATE ON managed_realtime_durable_cursors
 FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_channel_acknowledgement();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_channel_acknowledgement ON managed_realtime_durable_cursors;
DROP FUNCTION enqueue_realtime_channel_acknowledgement();
ALTER TABLE managed_realtime_durable_cursors DROP CONSTRAINT realtime_ack_pair,
 DROP COLUMN acknowledged_sequence, DROP COLUMN acknowledged_at;
-- Retain event vocabulary for queued deliveries.
