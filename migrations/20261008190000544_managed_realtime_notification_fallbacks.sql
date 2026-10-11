-- +goose Up
ALTER TABLE managed_realtime_inbox_messages ADD COLUMN IF NOT EXISTS fallback_after_seconds integer NOT NULL DEFAULT 0 CHECK (fallback_after_seconds BETWEEN 0 AND 86400);
-- Separate from payload retention: count eviction must not lose a pending timer.
CREATE TABLE IF NOT EXISTS managed_realtime_inbox_fallbacks (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK (principal ~ '^[0-9a-f]{64}$'),
 sequence bigint NOT NULL CHECK (sequence > 0),
 message_id text NOT NULL CHECK (length(message_id) BETWEEN 1 AND 128),
 deadline timestamptz NOT NULL,
 PRIMARY KEY(endpoint_id,principal,sequence)
);
CREATE INDEX IF NOT EXISTS managed_realtime_inbox_fallbacks_due_idx ON managed_realtime_inbox_fallbacks(deadline);

-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
   WHERE conrelid = 'app_webhook_event_outbox'::regclass AND conname = 'app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 definition := regexp_replace(definition, ' NOT VALID$', '');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event = %L)', substring(definition from 8 for length(definition)-8), 'realtime.inbox.fallback_required');
END $$;
-- +goose StatementEnd

-- Wrap the existing ACK transaction. Cursor locks serialize device retries;
-- fallback row locks serialize devices and cancellation against deadline processing.
-- +goose StatementBegin
DO $$ BEGIN
 IF to_regprocedure('faas_advance_realtime_inbox_cursor_before_fallback(uuid,text,text,bigint)') IS NULL THEN
  ALTER FUNCTION faas_advance_realtime_inbox_cursor(uuid,text,text,bigint) RENAME TO faas_advance_realtime_inbox_cursor_before_fallback;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_advance_realtime_inbox_cursor(ep uuid, pk text, device text, requested bigint)
RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE previous bigint; current_seq bigint; pending record;
BEGIN
 SELECT sequence INTO previous FROM managed_realtime_inbox_cursors
   WHERE endpoint_id = ep AND principal = pk AND subscription = device AND channel = pk FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 current_seq := faas_advance_realtime_inbox_cursor_before_fallback(ep,pk,device,requested);
 IF current_seq > previous THEN
   FOR pending IN SELECT sequence FROM managed_realtime_inbox_fallbacks
     WHERE endpoint_id = ep AND principal = pk AND sequence > previous AND sequence <= current_seq
     ORDER BY deadline, sequence FOR UPDATE
   LOOP
     DELETE FROM managed_realtime_inbox_fallbacks WHERE endpoint_id = ep AND principal = pk AND sequence = pending.sequence;
   END LOOP;
 END IF;
 RETURN current_seq;
END $$;
-- +goose StatementEnd

-- At-least-once signed dispatch uses the existing transactional app outbox.
-- Missing subscriptions leave deadlines pending instead of dropping fallback.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_drain_realtime_inbox_fallbacks(batch_size integer)
RETURNS integer LANGUAGE plpgsql AS $$
DECLARE f record; processed integer := 0; eid uuid;
BEGIN
 FOR f IN
   SELECT pending.*, ep.app_id, ep.account_id, hooks.recipients FROM managed_realtime_inbox_fallbacks pending
   JOIN managed_realtime_endpoints ep ON ep.id = pending.endpoint_id
   CROSS JOIN LATERAL (
     SELECT array_agg(h.id ORDER BY h.id) AS recipients FROM app_webhooks h
     WHERE h.app_id = ep.app_id AND h.account_id = ep.account_id
       AND h.scope = 'app' AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR 'realtime.inbox.fallback_required' = ANY(h.event_filter))
   ) hooks
   WHERE pending.deadline <= clock_timestamp() AND cardinality(hooks.recipients) > 0
   ORDER BY pending.deadline, pending.endpoint_id, pending.principal, pending.sequence
   LIMIT batch_size FOR UPDATE OF pending SKIP LOCKED
 LOOP
   eid := gen_random_uuid();
   INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
   VALUES(eid,f.account_id,f.app_id,'realtime.inbox.fallback_required',eid,
     jsonb_build_object('event_id',eid,'app_id',f.app_id,'endpoint_id',f.endpoint_id,
       'principal_key',f.principal,'consumer','','occurred_at',clock_timestamp(),
       'message_id',f.message_id,'sequence',f.sequence,'deadline',f.deadline),f.recipients);
   DELETE FROM managed_realtime_inbox_fallbacks WHERE endpoint_id = f.endpoint_id AND principal = f.principal AND sequence = f.sequence;
   processed := processed + 1;
 END LOOP;
 RETURN processed;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS faas_drain_realtime_inbox_fallbacks(integer);
DROP FUNCTION faas_advance_realtime_inbox_cursor(uuid,text,text,bigint);
ALTER FUNCTION faas_advance_realtime_inbox_cursor_before_fallback(uuid,text,text,bigint) RENAME TO faas_advance_realtime_inbox_cursor;
DROP TABLE managed_realtime_inbox_fallbacks;
ALTER TABLE managed_realtime_inbox_messages DROP COLUMN fallback_after_seconds;
-- Preserve the widened event vocabulary for historical deliveries.
