-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_enabled boolean NOT NULL DEFAULT false CHECK (circuit_enabled IN (true,false));
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_threshold integer NOT NULL DEFAULT 5 CHECK (circuit_threshold BETWEEN 1 AND 100);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_cooldown_seconds integer NOT NULL DEFAULT 30 CHECK (circuit_cooldown_seconds BETWEEN 1 AND 3600);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_failures integer NOT NULL DEFAULT 0 CHECK (circuit_failures BETWEEN 0 AND 100);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_state text NOT NULL DEFAULT 'closed' CHECK (circuit_state IN ('closed','open','half_open'));
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_open_until timestamptz CHECK (circuit_open_until IS NULL OR isfinite(circuit_open_until));
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_probe_sequence bigint CHECK (circuit_probe_sequence IS NULL OR circuit_probe_sequence>0);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_probe_fencing_token bigint CHECK (circuit_probe_fencing_token IS NULL OR circuit_probe_fencing_token>0);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS circuit_reason text NOT NULL DEFAULT 'configured' CHECK (circuit_reason IN ('configured','handler_failure','lease_expired','probe_failed','probe_success','cooldown_elapsed','probe_missing'));
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_groups') AND conname='realtime_circuit_shape') THEN
ALTER TABLE managed_realtime_consumer_groups ADD CONSTRAINT realtime_circuit_shape CHECK ((circuit_state='open')=(circuit_open_until IS NOT NULL) AND (circuit_probe_sequence IS NULL)=(circuit_probe_fencing_token IS NULL) AND (circuit_probe_sequence IS NULL OR circuit_state='half_open') AND (circuit_enabled OR circuit_state='closed'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('observe_realtime_circuit(uuid,bigint,bigint,boolean,text)') IS NULL THEN
CREATE FUNCTION observe_realtime_circuit(gid uuid, seq bigint, fence bigint, success boolean, reason text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE g managed_realtime_consumer_groups%ROWTYPE;
BEGIN
 SELECT * INTO g FROM managed_realtime_consumer_groups WHERE id=gid FOR UPDATE;
 IF NOT FOUND OR NOT g.circuit_enabled THEN RETURN; END IF;
 IF g.circuit_state='half_open' AND g.circuit_probe_sequence=seq AND g.circuit_probe_fencing_token=fence THEN
  UPDATE managed_realtime_consumer_groups SET circuit_state=CASE WHEN success THEN 'closed' ELSE 'open' END,
   circuit_failures=CASE WHEN success THEN 0 ELSE circuit_threshold END,
   circuit_open_until=CASE WHEN success THEN NULL ELSE clock_timestamp()+make_interval(secs=>circuit_cooldown_seconds) END,
   circuit_probe_sequence=NULL,circuit_probe_fencing_token=NULL,circuit_reason=CASE WHEN success THEN 'probe_success' ELSE 'probe_failed' END WHERE id=gid;
 ELSIF g.circuit_state='closed' THEN
  IF success THEN UPDATE managed_realtime_consumer_groups SET circuit_failures=0 WHERE id=gid;
  ELSIF reason IN ('handler_failure','lease_expired') THEN
   UPDATE managed_realtime_consumer_groups SET circuit_failures=least(circuit_threshold,circuit_failures+1),
    circuit_state=CASE WHEN circuit_failures+1>=circuit_threshold THEN 'open' ELSE 'closed' END,
    circuit_open_until=CASE WHEN circuit_failures+1>=circuit_threshold THEN clock_timestamp()+make_interval(secs=>circuit_cooldown_seconds) ELSE NULL END,
    circuit_reason=reason WHERE id=gid;
  END IF;
 END IF;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('observe_realtime_circuit_failure()') IS NULL THEN
CREATE FUNCTION observe_realtime_circuit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='leased' AND NEW.status IN ('ready','dead') THEN
  PERFORM observe_realtime_circuit(NEW.group_id,NEW.sequence,OLD.fencing_token,false,
   CASE WHEN NEW.failure_code='lease_expired' THEN 'lease_expired'
        WHEN NEW.deadline_invalid OR (NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp()) THEN 'deadline'
        ELSE 'handler_failure' END);
 END IF;
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_circuit_failure') THEN
CREATE TRIGGER realtime_circuit_failure AFTER UPDATE ON managed_realtime_consumer_group_messages FOR EACH ROW EXECUTE FUNCTION observe_realtime_circuit_failure();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.circuit_closed''' in definition)>0 AND position('''realtime.consumer_group.circuit_half_open''' in definition)>0 AND position('''realtime.consumer_group.circuit_opened''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.circuit_opened','realtime.consumer_group.circuit_half_open','realtime.consumer_group.circuit_closed');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_circuit_transition()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_circuit_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; event_id uuid; event_name text;
BEGIN
 IF NEW.circuit_state=OLD.circuit_state THEN RETURN NEW; END IF;
 event_name:=CASE NEW.circuit_state WHEN 'open' THEN 'realtime.consumer_group.circuit_opened' WHEN 'half_open' THEN 'realtime.consumer_group.circuit_half_open' ELSE 'realtime.consumer_group.circuit_closed' END;
 SELECT e.app_id,e.account_id,array_agg(h.id ORDER BY h.id) INTO v_app,v_account,recipients
 FROM managed_realtime_endpoints e JOIN app_webhooks h ON h.account_id=e.account_id AND h.app_id=e.app_id
 WHERE e.id=NEW.endpoint_id AND h.scope='app' AND h.enabled AND event_name=ANY(h.event_filter) GROUP BY e.app_id,e.account_id;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,event_name,event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',NEW.endpoint_id,'group_id',NEW.id,'channel',NEW.channel,'group',NEW.name,
 'state',NEW.circuit_state,'enabled',NEW.circuit_enabled,'reason',NEW.circuit_reason,'consecutive_failures',NEW.circuit_failures,'failure_threshold',NEW.circuit_threshold,'cooldown_seconds',NEW.circuit_cooldown_seconds,'next_probe_at',NEW.circuit_open_until,'occurred_at',clock_timestamp()),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_groups') AND tgname='realtime_circuit_transition') THEN
CREATE TRIGGER realtime_circuit_transition AFTER UPDATE ON managed_realtime_consumer_groups FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_circuit_transition();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_circuit_transition ON managed_realtime_consumer_groups;
DROP FUNCTION enqueue_realtime_circuit_transition();
DROP TRIGGER realtime_circuit_failure ON managed_realtime_consumer_group_messages;
DROP FUNCTION observe_realtime_circuit_failure();
DROP FUNCTION observe_realtime_circuit(uuid,bigint,bigint,boolean,text);
ALTER TABLE managed_realtime_consumer_groups DROP CONSTRAINT realtime_circuit_shape,DROP COLUMN circuit_enabled,DROP COLUMN circuit_threshold,DROP COLUMN circuit_cooldown_seconds,DROP COLUMN circuit_failures,DROP COLUMN circuit_state,DROP COLUMN circuit_open_until,DROP COLUMN circuit_probe_sequence,DROP COLUMN circuit_probe_fencing_token,DROP COLUMN circuit_reason;
-- Queued webhook vocabulary remains. Upgrade/downgrade server binaries with schema.
