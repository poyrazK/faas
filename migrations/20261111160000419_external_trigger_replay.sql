-- filename: 20261111160000419_external_trigger_replay.sql

-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE trigger_records ADD COLUMN IF NOT EXISTS operator_replay boolean NOT NULL DEFAULT false;

-- Recover unified replays already acknowledged by the old implementation.
-- Commit each bounded batch so active dispatch does not wait for the backfill.
-- +goose StatementBegin
DO $backfill$
DECLARE marked bigint;
BEGIN
 LOOP
  UPDATE trigger_records SET operator_replay=true
  WHERE id IN (
   SELECT r.id FROM trigger_records r JOIN triggers t ON t.id=r.trigger_id
   WHERE NOT r.operator_replay AND r.state IN ('pending','retry')
    AND t.kind IN ('kafka','nats','redis_streams')
    AND NOT (r.metadata ? 'payload_omitted_bytes')
    AND EXISTS (SELECT 1 FROM dead_letter_events d
     WHERE d.source='trigger_record' AND d.source_id=r.id AND d.replayed_at IS NOT NULL)
   ORDER BY r.id LIMIT 1000 FOR UPDATE OF r);
  GET DIAGNOSTICS marked = ROW_COUNT;
  COMMIT;
  EXIT WHEN marked = 0;
 END LOOP;
END $backfill$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS trigger_operator_replay_due_idx
ON trigger_records(trigger_id, next_fire_at, id)
WHERE operator_replay AND state IN ('pending', 'retry');

-- Older constructors retained token-style URL userinfo in diagnostic errors.
-- Preserve the failure context while removing the credentials from this cache.
-- +goose StatementBegin
DO $redact$
DECLARE marked bigint;
BEGIN
 LOOP
  UPDATE trigger_consumer_health
  SET last_error = regexp_replace(last_error, '((nats|tls)://)[^[:space:]/]*@', '\1', 'g')
  WHERE trigger_id IN (
   SELECT trigger_id FROM trigger_consumer_health
   WHERE last_error ~ '(nats|tls)://[^[:space:]/]*@'
   ORDER BY trigger_id LIMIT 1000 FOR UPDATE);
  GET DIAGNOSTICS marked = ROW_COUNT;
  COMMIT;
  EXIT WHEN marked = 0;
 END LOOP;
END $redact$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
  LOCK TABLE trigger_records IN ACCESS EXCLUSIVE MODE;
  IF EXISTS (SELECT 1 FROM trigger_records WHERE operator_replay AND state IN ('pending','retry','claimed')) THEN
    RAISE EXCEPTION 'cannot remove unsettled operator trigger replays' USING ERRCODE='55000';
  END IF;
  DROP INDEX trigger_operator_replay_due_idx;
  ALTER TABLE trigger_records DROP COLUMN operator_replay;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
