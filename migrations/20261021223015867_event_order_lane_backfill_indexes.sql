-- migration-compat: expand
-- filename: 20261021223015867_event_order_lane_backfill_indexes.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- Only unsettled recipients can block a later delivery, so only they need a
-- lane before the ordering check reads it (20261021223015891). The trigger
-- from 20261021223015842 already covers rows written from here on; each
-- batch commits so live writers never queue behind the backfill.
DO $backfill$
DECLARE ids bigint[];
BEGIN
 LOOP
  PERFORM set_config('lock_timeout', '5s', true);
  SELECT array_agg(x.outbox_id) INTO ids FROM (
   SELECT DISTINCT r.outbox_id FROM event_fanout_recipients r
   JOIN event_fanout_outbox o ON o.id = r.outbox_id
   WHERE r.state IN ('pending', 'processing') AND r.order_lane IS NULL
     AND coalesce(r.recipient->'work'->>'policy_name', '') <> ''
     AND event_order_lane_lock_id(r.recipient->>'app_id', r.recipient->'work'->>'policy_name',
         o.payload, r.recipient->'work'->>'key_selector') IS NOT NULL
   LIMIT 1000) x;
  EXIT WHEN ids IS NULL;
  UPDATE event_fanout_recipients r
  SET order_lane = event_order_lane_lock_id(r.recipient->>'app_id', r.recipient->'work'->>'policy_name',
      o.payload, r.recipient->'work'->>'key_selector')
  FROM event_fanout_outbox o
  WHERE r.outbox_id = ANY (ids) AND o.id = r.outbox_id AND r.state IN ('pending', 'processing')
    AND r.order_lane IS NULL AND coalesce(r.recipient->'work'->>'policy_name', '') <> '';
  COMMIT;
 END LOOP;
END $backfill$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Earlier unsettled deliveries in one ordering lane. Rows leave the index
-- when they settle.
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_fanout_recipients_order_lane_idx
    ON event_fanout_recipients (order_lane, outbox_id)
    WHERE order_lane IS NOT NULL AND state IN ('pending', 'processing');
-- +goose StatementEnd

-- +goose StatementBegin
-- Unsettled receipts not yet adopted for independent routing: the part of
-- the ordering check that still reads receipts rather than recipients.
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_fanout_order_unadopted_idx
    ON event_fanout_outbox (account_id, id)
    WHERE state <> 'delivered' AND NOT recipient_claims;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS event_fanout_order_unadopted_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS event_fanout_recipients_order_lane_idx;
-- +goose StatementEnd
