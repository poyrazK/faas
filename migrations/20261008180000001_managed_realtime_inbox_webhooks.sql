-- +goose Up
ALTER TABLE managed_realtime_inbox_cursors ADD COLUMN gap_reported boolean NOT NULL DEFAULT false;
CREATE INDEX managed_realtime_inbox_gap_candidates_idx ON managed_realtime_inbox_cursors(updated_at, endpoint_id) WHERE NOT gap_reported;

-- Preserve the full event vocabulary installed by earlier migrations.
-- +goose StatementBegin
DO $$
DECLARE t text; n text; definition text;
BEGIN
  FOREACH t IN ARRAY ARRAY['app_webhook_event_outbox'] LOOP
    n := t || '_event_chk';
    SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
      WHERE conrelid = t::regclass AND conname = n;
    IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint %', n; END IF;
    definition := regexp_replace(definition, ' NOT VALID$', '');
    EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', t, n);
    EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK ((%s) OR event IN (%L, %L))',
      t, n, substring(definition from 8 for length(definition)-8),
      'realtime.inbox.acknowledged', 'realtime.inbox.gap');
  END LOOP;
END $$;
-- +goose StatementEnd

-- Recipient snapshot and stable event ID are committed with the checkpoint.
-- +goose StatementBegin
CREATE FUNCTION faas_capture_realtime_inbox_webhook(ep uuid, principal_key text, consumer text, event_name text, details jsonb)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE a uuid; account uuid; recipients uuid[]; eid uuid := gen_random_uuid();
BEGIN
  SELECT app_id, account_id INTO a, account FROM managed_realtime_endpoints WHERE id = ep;
  SELECT array_agg(id ORDER BY id) INTO recipients FROM app_webhooks
    WHERE app_id = a AND account_id = account AND scope = 'app' AND enabled
      AND (cardinality(event_filter) = 0 OR event_name = ANY(event_filter));
  IF coalesce(cardinality(recipients), 0) = 0 THEN RETURN; END IF;
  INSERT INTO app_webhook_event_outbox(id, account_id, app_id, event, source_id, payload, recipient_webhook_ids)
    VALUES(eid, account, a, event_name, eid,
      details || jsonb_build_object('event_id', eid, 'app_id', a, 'endpoint_id', ep,
        'principal_key', principal_key, 'consumer', consumer, 'occurred_at', clock_timestamp()), recipients);
END $$;
-- +goose StatementEnd

-- Only advancing ACKs emit an event; reconnects, duplicate ACKs and resets do not.
-- +goose StatementBegin
CREATE FUNCTION faas_advance_realtime_inbox_cursor(ep uuid, pk text, device text, requested bigint)
RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE previous bigint; current_seq bigint; mid text; floor_seq bigint;
BEGIN
  SELECT sequence INTO previous FROM managed_realtime_inbox_cursors
    WHERE endpoint_id = ep AND principal = pk AND subscription = device AND channel = pk FOR UPDATE;
  IF NOT FOUND THEN RETURN NULL; END IF;
  current_seq := greatest(previous, requested);
  SELECT greatest(h.oldest_sequence, coalesce((SELECT max(m.sequence) + 1
    FROM managed_realtime_inbox_messages m WHERE m.endpoint_id = ep AND m.channel = pk
      AND m.created_at < clock_timestamp() - interval '24 hours'), 1))
    INTO floor_seq FROM managed_realtime_inbox_heads h WHERE h.endpoint_id = ep AND h.channel = pk;
  UPDATE managed_realtime_inbox_cursors SET sequence = current_seq, updated_at = clock_timestamp(),
    gap_reported = CASE WHEN current_seq >= floor_seq - 1 THEN false ELSE gap_reported END
    WHERE endpoint_id = ep AND principal = pk AND subscription = device AND channel = pk;
  IF current_seq > previous THEN
    SELECT idempotency_key INTO mid FROM managed_realtime_inbox_messages
      WHERE endpoint_id = ep AND channel = pk AND sequence = current_seq;
    PERFORM faas_capture_realtime_inbox_webhook(ep, pk, device, 'realtime.inbox.acknowledged',
      jsonb_build_object('previous_sequence', previous, 'sequence', current_seq, 'message_id', mid));
  END IF;
  RETURN current_seq;
END $$;
-- +goose StatementEnd

-- Detect offline devices too. One notification per gap episode, cleared by an
-- explicit reset, with a bounded batch and row locks for multiple API workers.
-- +goose StatementBegin
CREATE FUNCTION faas_scan_realtime_inbox_gaps(batch_size integer)
RETURNS integer LANGUAGE plpgsql AS $$
DECLARE c record; processed integer := 0;
BEGIN
  FOR c IN
    SELECT cur.*, h.next_sequence - 1 AS latest,
      greatest(h.oldest_sequence, coalesce(expired.floor, 1)) AS oldest
    FROM managed_realtime_inbox_cursors cur
    JOIN managed_realtime_inbox_heads h ON h.endpoint_id = cur.endpoint_id AND h.channel = cur.channel
    LEFT JOIN LATERAL (
      SELECT max(sequence) + 1 AS floor FROM managed_realtime_inbox_messages m
      WHERE m.endpoint_id = cur.endpoint_id AND m.channel = cur.channel
        AND m.created_at < clock_timestamp() - interval '24 hours'
    ) expired ON true
    WHERE NOT cur.gap_reported AND cur.updated_at >= clock_timestamp() - interval '30 days'
      AND cur.sequence < greatest(h.oldest_sequence, coalesce(expired.floor, 1)) - 1
    ORDER BY cur.updated_at, cur.endpoint_id, cur.principal, cur.subscription
    LIMIT batch_size FOR UPDATE OF cur SKIP LOCKED
  LOOP
    PERFORM faas_capture_realtime_inbox_webhook(c.endpoint_id, c.principal, c.subscription, 'realtime.inbox.gap',
      jsonb_build_object('sequence', c.sequence, 'oldest_sequence', c.oldest, 'latest_sequence', c.latest));
    UPDATE managed_realtime_inbox_cursors SET gap_reported = true
      WHERE endpoint_id = c.endpoint_id AND principal = c.principal AND subscription = c.subscription AND channel = c.channel;
    processed := processed + 1;
  END LOOP;
  RETURN processed;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS faas_scan_realtime_inbox_gaps(integer);
DROP FUNCTION IF EXISTS faas_advance_realtime_inbox_cursor(uuid, text, text, bigint);
DROP FUNCTION IF EXISTS faas_capture_realtime_inbox_webhook(uuid, text, text, text, jsonb);
ALTER TABLE managed_realtime_inbox_cursors DROP COLUMN gap_reported;
-- Keep the widened ledger vocabulary so historical deliveries remain replayable.
