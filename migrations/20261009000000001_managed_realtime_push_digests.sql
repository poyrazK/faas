-- +goose Up
ALTER TABLE managed_realtime_inbox_messages ADD COLUMN notification_group_key text NOT NULL DEFAULT '' CHECK(octet_length(notification_group_key)<=128);
ALTER TABLE managed_realtime_inbox_messages ADD COLUMN notification_group_label text NOT NULL DEFAULT '' CHECK(octet_length(notification_group_label)<=128);
ALTER TABLE managed_realtime_inbox_fallbacks ADD COLUMN group_key text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_inbox_fallbacks ADD COLUMN group_label text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_push_deliveries ADD COLUMN group_key text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_push_deliveries ADD COLUMN group_label text NOT NULL DEFAULT '';
ALTER TABLE managed_realtime_push_deliveries ADD COLUMN digest_id uuid;
ALTER TABLE managed_realtime_push_deliveries ADD COLUMN digest_count integer NOT NULL DEFAULT 0;
ALTER TABLE managed_realtime_push_deliveries ADD COLUMN digest_at timestamptz NOT NULL DEFAULT 'epoch';
CREATE INDEX managed_realtime_push_digest_members ON managed_realtime_push_deliveries(digest_id,id) WHERE digest_id IS NOT NULL;
CREATE INDEX managed_realtime_push_group_pending ON managed_realtime_push_deliveries(endpoint_id,principal,device,version,category,group_key) WHERE digest_id IS NULL AND status IN ('pending','sending');
ALTER FUNCTION faas_drain_realtime_inbox_fallbacks(integer) RENAME TO faas_drain_realtime_inbox_fallbacks_before_digests;
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
   INSERT INTO managed_realtime_push_deliveries(endpoint_id,principal,device,provider,version,message_id,sequence,category,group_key,group_label)
   SELECT f.endpoint_id,f.principal,x->>'device',x->>'provider',(x->>'version')::bigint,f.message_id,f.sequence,f.category,f.group_key,f.group_label FROM jsonb_array_elements(devices) x
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
ALTER FUNCTION faas_drain_realtime_inbox_fallbacks_before_digests(integer) RENAME TO faas_drain_realtime_inbox_fallbacks;
DROP INDEX managed_realtime_push_group_pending;
DROP INDEX managed_realtime_push_digest_members;
ALTER TABLE managed_realtime_push_deliveries DROP COLUMN digest_at;
ALTER TABLE managed_realtime_push_deliveries DROP COLUMN digest_count;
ALTER TABLE managed_realtime_push_deliveries DROP COLUMN digest_id;
ALTER TABLE managed_realtime_push_deliveries DROP COLUMN group_key;
ALTER TABLE managed_realtime_push_deliveries DROP COLUMN group_label;
ALTER TABLE managed_realtime_inbox_fallbacks DROP COLUMN group_key;
ALTER TABLE managed_realtime_inbox_fallbacks DROP COLUMN group_label;
ALTER TABLE managed_realtime_inbox_messages DROP COLUMN notification_group_key;
ALTER TABLE managed_realtime_inbox_messages DROP COLUMN notification_group_label;
