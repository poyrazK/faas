-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS next_worker_health_check_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_worker_health_check_at));
CREATE TABLE IF NOT EXISTS managed_realtime_worker_heartbeats (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 worker text NOT NULL CHECK (octet_length(worker) BETWEEN 1 AND 128),
 principal text NOT NULL CHECK (principal='account' OR principal ~ '^[0-9a-f-]{36}$'),
 credential_id uuid,
 ready boolean NOT NULL CHECK (ready IN (true,false)),
 status_code text NOT NULL CHECK (octet_length(status_code)<=64 AND status_code ~ '^[ -~]*$'),
 last_seen_at timestamptz NOT NULL CHECK (isfinite(last_seen_at)),
 lost boolean NOT NULL DEFAULT false CHECK (lost IN (true,false)),
 CHECK ((principal='account')=(credential_id IS NULL)),
 UNIQUE(group_id,worker,principal)
);
CREATE INDEX IF NOT EXISTS realtime_worker_health_due ON managed_realtime_consumer_groups(next_worker_health_check_at,id);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_permissions_check' AND position('''heartbeat''' in pg_get_constraintdef(oid))>0) OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_last_operation_check' AND position('''heartbeat''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_permissions_check,DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK (cardinality(permissions) BETWEEN 1 AND 13 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.worker_lost''' in definition)>0 AND position('''realtime.consumer_group.worker_recovered''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.worker_lost','realtime.consumer_group.worker_recovered');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('enqueue_realtime_worker_health()') IS NULL THEN
CREATE FUNCTION enqueue_realtime_worker_health() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; v_app uuid; v_account uuid; v_endpoint uuid; v_channel text; v_group text; event_id uuid; event_name text;
BEGIN
 IF NEW.lost=OLD.lost THEN RETURN NEW; END IF;
 event_name:=CASE WHEN NEW.lost THEN 'realtime.consumer_group.worker_lost' ELSE 'realtime.consumer_group.worker_recovered' END;
 SELECT e.app_id,e.account_id,g.endpoint_id,g.channel,g.name,array_agg(h.id ORDER BY h.id)
 INTO v_app,v_account,v_endpoint,v_channel,v_group,recipients
 FROM managed_realtime_consumer_groups g JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id
 JOIN app_webhooks h ON h.account_id=e.account_id AND h.app_id=e.app_id
 WHERE g.id=NEW.group_id AND h.scope='app' AND h.enabled AND event_name=ANY(h.event_filter)
 GROUP BY e.app_id,e.account_id,g.endpoint_id,g.channel,g.name;
 IF recipients IS NULL THEN RETURN NEW; END IF;
 event_id:=gen_random_uuid();
 INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES(event_id,v_account,v_app,event_name,event_id,
 jsonb_build_object('event_id',event_id,'app_id',v_app,'endpoint_id',v_endpoint,'group_id',NEW.group_id,'channel',v_channel,'group',v_group,
 'heartbeat_id',NEW.id,'worker',NEW.worker,'ready',NEW.ready,'status_code',NEW.status_code,'last_seen_at',NEW.last_seen_at,'occurred_at',clock_timestamp()),recipients);
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_worker_heartbeats') AND tgname='realtime_worker_health_transition') THEN
CREATE TRIGGER realtime_worker_health_transition AFTER UPDATE ON managed_realtime_worker_heartbeats FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_worker_health();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
-- Existing heartbeat grants/last-operation values intentionally block unsafe downgrade.
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT managed_realtime_worker_credentials_permissions_check,DROP CONSTRAINT managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK (cardinality(permissions) BETWEEN 1 AND 12 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain'));
DROP TRIGGER realtime_worker_health_transition ON managed_realtime_worker_heartbeats;
DROP FUNCTION enqueue_realtime_worker_health();
DROP TABLE managed_realtime_worker_heartbeats;
DROP INDEX realtime_worker_health_due;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN next_worker_health_check_at;
-- Queued webhook vocabulary remains.
