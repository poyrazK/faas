-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L,%L,%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.notification.sent','realtime.notification.failed','realtime.notification.expired','realtime.notification.cancelled','realtime.notification.superseded');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION faas_emit_notification_outcome() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_name text; recipients uuid[]; eid uuid; app uuid; account uuid;
BEGIN
 IF NEW.status NOT IN ('sent','failed','cancelled') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.status=NEW.status THEN RETURN NEW; END IF;
 event_name:=CASE WHEN NEW.status='sent' THEN 'realtime.notification.sent'
  WHEN NEW.status='failed' THEN 'realtime.notification.failed'
  WHEN NEW.code IN ('expired','quiet_hours_expired') THEN 'realtime.notification.expired'
  WHEN NEW.code='superseded' THEN 'realtime.notification.superseded'
  ELSE 'realtime.notification.cancelled' END;
 SELECT app_id,account_id INTO app,account FROM managed_realtime_endpoints WHERE id=NEW.endpoint_id;
 IF app IS NULL THEN RETURN NEW; END IF;
 SELECT array_agg(h.id ORDER BY h.id) INTO recipients FROM app_webhooks h
  WHERE h.app_id=app AND h.account_id=account AND h.scope='app' AND h.enabled AND (cardinality(h.event_filter)=0 OR event_name=ANY(h.event_filter));
 IF coalesce(cardinality(recipients),0)=0 THEN RETURN NEW; END IF;
 eid:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(eid,account,app,event_name,eid,jsonb_build_object('event_id',eid,'app_id',app,'endpoint_id',NEW.endpoint_id,'principal_key',NEW.principal,'consumer','','occurred_at',clock_timestamp(),'delivery_id',NEW.id,'message_id',NEW.message_id,'sequence',NEW.sequence,'device',NEW.device,'provider',NEW.provider,'status',NEW.status,'reason',NEW.code,'attempts',NEW.attempts,'status_code',NEW.status_code,'category',NEW.category,'priority',NEW.priority,'digest_id',coalesce(NEW.digest_id::text,'')),recipients);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER managed_realtime_notification_outcome AFTER INSERT OR UPDATE ON managed_realtime_push_deliveries FOR EACH ROW EXECUTE FUNCTION faas_emit_notification_outcome();
-- +goose Down
DROP TRIGGER managed_realtime_notification_outcome ON managed_realtime_push_deliveries;
DROP FUNCTION faas_emit_notification_outcome();
-- Preserve event vocabulary so historical outbox rows remain replayable.
