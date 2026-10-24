-- migration-compat: expand
-- filename: 20261023110719517_event_delivery_live_slots_backfill.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- Slots written before 20261023110719283 all say live. Marking one dead is
-- the only write that could undercount a delivery limit, so each batch locks
-- its invocations and re-reads their state: a replay that makes a delivery
-- live again waits for the batch, then sets the slot live through the
-- trigger. Each batch commits, so admissions never queue behind the
-- backfill.
DO $backfill$
DECLARE marked bigint;
BEGIN
 LOOP
  PERFORM set_config('lock_timeout', '5s', true);
  UPDATE event_delivery_slots SET live = false
  WHERE invocation_id IN (
   SELECT s.invocation_id FROM event_delivery_slots s JOIN invocations i ON i.id = s.invocation_id
   WHERE s.live AND i.state NOT IN ('pending', 'dispatching')
   LIMIT 1000 FOR SHARE OF i);
  GET DIAGNOSTICS marked = ROW_COUNT;
  COMMIT;
  EXIT WHEN marked = 0;
 END LOOP;
END $backfill$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The account's live deliveries: what every delivery-limit count reads.
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_delivery_slots_live
    ON event_delivery_slots (account_id, app_id, subscription_id) WHERE live;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS event_delivery_slots_live;
-- +goose StatementEnd
