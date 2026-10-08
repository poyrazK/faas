-- +goose Up
CREATE TABLE managed_realtime_push_providers (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 provider text NOT NULL CHECK(provider IN ('fcm','apns','webpush')),
 enabled boolean NOT NULL DEFAULT true,
 sealed bytea NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,provider)
);
CREATE SEQUENCE managed_realtime_push_device_version;
CREATE TABLE managed_realtime_push_devices (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK(principal ~ '^[0-9a-f]{64}$'),
 device text NOT NULL,
 provider text NOT NULL CHECK(provider IN ('fcm','apns','webpush')),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT nextval('managed_realtime_push_device_version'),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 sealed bytea NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,principal,device)
);
CREATE TABLE managed_realtime_push_deliveries (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL,
 device text NOT NULL,
 provider text NOT NULL,
 version bigint NOT NULL,
 message_id text NOT NULL,
 sequence bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','cancelled')),
 attempts integer NOT NULL DEFAULT 0,
 status_code integer NOT NULL DEFAULT 0,
 code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 next_attempt timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease uuid,
 lease_until timestamptz,
 UNIQUE(endpoint_id,principal,sequence,device)
);
CREATE INDEX managed_realtime_push_due ON managed_realtime_push_deliveries(next_attempt) WHERE status IN ('pending','sending');
CREATE INDEX managed_realtime_push_expiry ON managed_realtime_push_deliveries(updated_at) WHERE status IN ('sent','failed','cancelled');
CREATE INDEX managed_realtime_push_history ON managed_realtime_push_deliveries(endpoint_id,principal,created_at DESC);

ALTER FUNCTION faas_advance_realtime_inbox_cursor(uuid,text,text,bigint) RENAME TO faas_advance_realtime_inbox_cursor_before_push;
-- +goose StatementBegin
CREATE FUNCTION faas_advance_realtime_inbox_cursor(ep uuid, pk text, device text, requested bigint)
RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE previous bigint; current_seq bigint;
BEGIN
 SELECT sequence INTO previous FROM managed_realtime_inbox_cursors
 WHERE endpoint_id=ep AND principal=pk AND subscription=device AND channel=pk FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 current_seq:=faas_advance_realtime_inbox_cursor_before_push(ep,pk,device,requested);
 UPDATE managed_realtime_push_deliveries SET status='cancelled',code='acknowledged',updated_at=clock_timestamp(),lease=NULL,lease_until=NULL
 WHERE endpoint_id=ep AND principal=pk AND sequence>previous AND sequence<=current_seq AND status IN ('pending','sending');
 RETURN current_seq;
END $$;
-- +goose StatementEnd

ALTER FUNCTION faas_drain_realtime_inbox_fallbacks(integer) RENAME TO faas_drain_realtime_inbox_fallbacks_before_push;
-- +goose StatementBegin
CREATE FUNCTION faas_drain_realtime_inbox_fallbacks(batch_size integer)
RETURNS integer LANGUAGE plpgsql AS $$
DECLARE f record; recipients uuid[]; devices jsonb; processed integer:=0; total integer; eid uuid;
BEGIN
 FOR f IN SELECT pending.*,ep.app_id,ep.account_id FROM managed_realtime_inbox_fallbacks pending
 JOIN managed_realtime_endpoints ep ON ep.id=pending.endpoint_id AND ep.enabled
 WHERE pending.deadline<=clock_timestamp() AND (
 EXISTS(SELECT 1 FROM app_webhooks h WHERE h.app_id=ep.app_id AND h.account_id=ep.account_id AND h.scope='app' AND h.enabled AND (cardinality(h.event_filter)=0 OR 'realtime.inbox.fallback_required'=ANY(h.event_filter)))
 OR EXISTS(SELECT 1 FROM managed_realtime_push_devices d JOIN managed_realtime_push_providers p USING(endpoint_id,provider) WHERE d.endpoint_id=ep.id AND d.principal=pending.principal AND d.enabled AND p.enabled))
 AND (SELECT count(*) FROM managed_realtime_push_deliveries j WHERE j.endpoint_id=ep.id AND j.status IN ('pending','sending'))
   +(SELECT count(*) FROM managed_realtime_push_devices d JOIN managed_realtime_push_providers p USING(endpoint_id,provider) WHERE d.endpoint_id=ep.id AND d.principal=pending.principal AND d.enabled AND p.enabled)<=4096
 ORDER BY pending.deadline,pending.endpoint_id,pending.principal,pending.sequence
 LIMIT batch_size FOR UPDATE OF pending SKIP LOCKED
 LOOP
   -- A separate advisory lock serializes queue capacity without locking endpoint
   -- rows after inbox/fallback rows (append acquires these in the opposite order).
   IF NOT pg_try_advisory_xact_lock(hashtextextended('realtime-push:'||f.endpoint_id::text,0)) THEN CONTINUE; END IF;
   SELECT array_agg(h.id ORDER BY h.id) INTO recipients FROM app_webhooks h
   WHERE h.app_id=f.app_id AND h.account_id=f.account_id AND h.scope='app' AND h.enabled
   AND (cardinality(h.event_filter)=0 OR 'realtime.inbox.fallback_required'=ANY(h.event_filter));
   SELECT coalesce(jsonb_agg(jsonb_build_object('device',d.device,'provider',d.provider,'version',d.version)),'[]'::jsonb) INTO devices
   FROM managed_realtime_push_devices d JOIN managed_realtime_push_providers p USING(endpoint_id,provider)
   WHERE d.endpoint_id=f.endpoint_id AND d.principal=f.principal AND d.enabled AND p.enabled;
   IF coalesce(cardinality(recipients),0)=0 AND jsonb_array_length(devices)=0 THEN CONTINUE; END IF;
   SELECT count(*) INTO total FROM managed_realtime_push_deliveries WHERE endpoint_id=f.endpoint_id;
   IF total+jsonb_array_length(devices)>4096 THEN
     DELETE FROM managed_realtime_push_deliveries WHERE id IN (
       SELECT id FROM managed_realtime_push_deliveries WHERE endpoint_id=f.endpoint_id AND status IN ('sent','failed','cancelled')
       ORDER BY updated_at,id LIMIT total+jsonb_array_length(devices)-4096 FOR UPDATE SKIP LOCKED);
     SELECT count(*) INTO total FROM managed_realtime_push_deliveries WHERE endpoint_id=f.endpoint_id;
     IF total+jsonb_array_length(devices)>4096 THEN CONTINUE; END IF;
   END IF;
   INSERT INTO managed_realtime_push_deliveries(endpoint_id,principal,device,provider,version,message_id,sequence)
   SELECT f.endpoint_id,f.principal,x->>'device',x->>'provider',(x->>'version')::bigint,f.message_id,f.sequence FROM jsonb_array_elements(devices) x
   ON CONFLICT(endpoint_id,principal,sequence,device) DO NOTHING;
   IF coalesce(cardinality(recipients),0)>0 THEN
     eid:=gen_random_uuid();
     INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
     VALUES(eid,f.account_id,f.app_id,'realtime.inbox.fallback_required',eid,
       jsonb_build_object('event_id',eid,'app_id',f.app_id,'endpoint_id',f.endpoint_id,'principal_key',f.principal,'consumer','','occurred_at',clock_timestamp(),'message_id',f.message_id,'sequence',f.sequence,'deadline',f.deadline),recipients);
   END IF;
   DELETE FROM managed_realtime_inbox_fallbacks WHERE endpoint_id=f.endpoint_id AND principal=f.principal AND sequence=f.sequence;
   processed:=processed+1;
 END LOOP;
 RETURN processed;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION faas_drain_realtime_inbox_fallbacks(integer);
ALTER FUNCTION faas_drain_realtime_inbox_fallbacks_before_push(integer) RENAME TO faas_drain_realtime_inbox_fallbacks;
DROP FUNCTION faas_advance_realtime_inbox_cursor(uuid,text,text,bigint);
ALTER FUNCTION faas_advance_realtime_inbox_cursor_before_push(uuid,text,text,bigint) RENAME TO faas_advance_realtime_inbox_cursor;
DROP TABLE managed_realtime_push_deliveries;
DROP TABLE managed_realtime_push_devices;
DROP SEQUENCE managed_realtime_push_device_version;
DROP TABLE managed_realtime_push_providers;
