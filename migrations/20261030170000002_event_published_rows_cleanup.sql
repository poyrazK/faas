-- filename: 20261030170000002_event_published_rows_cleanup.sql
-- migration-compat: expand

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- ADR-20261030170000: published events are no longer stored in events (see
-- 20261030170000001); remove the copies earlier releases left there. Their
-- receipts hold the same envelopes until the plan's window. Each batch reads
-- one day through events_at_brin_idx and commits, so the shared audit table
-- never holds a long transaction. Rows older than the 90-day audit retention
-- are already gone.
DO $cleanup$
DECLARE day_start timestamptz; removed bigint;
BEGIN
 FOR day_start IN SELECT generate_series(date_trunc('day', now() - interval '100 days'), date_trunc('day', now()), interval '1 day') LOOP
  LOOP
   PERFORM set_config('lock_timeout', '5s', true);
   DELETE FROM events WHERE ctid = ANY (ARRAY(
     SELECT ctid FROM events
      WHERE at >= day_start AND at < day_start + interval '1 day' AND kind = 'event.published'
      LIMIT 1000));
   GET DIAGNOSTICS removed = ROW_COUNT;
   COMMIT;
   EXIT WHEN removed = 0;
  END LOOP;
 END LOOP;
END $cleanup$;
-- +goose StatementEnd

-- +goose Down
-- Removed copies are not restored; their receipts are the record.
