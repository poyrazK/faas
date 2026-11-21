-- filename: 20261112114342618_object_inventory_schedule_backlog.sql
-- migration-compat: expand
-- Each step is its own statement (one transaction, at most one table lock).
-- Re-runnable after a partial run. The copy runs after the trigger, so every
-- activity change before it is covered by what it reads and every one after
-- it by the trigger.
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261111152144 §4: the inventory backlog gauges read the schedule.
-- The schedule copies object_bucket_activity.active_since, so a bucket whose
-- 900 s window starts when it turned active (active_since after a
-- current-scope observation) is an index predicate, and gains indexes for
-- the oldest active observation and the oldest activation. Usage rows with
-- a claim token get a small index so the leased buckets can be listed.
SET lock_timeout = '5s';
SET statement_timeout = 0;

ALTER TABLE object_inventory_schedule ADD COLUMN IF NOT EXISTS active_since timestamptz NOT NULL DEFAULT '-infinity';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_inventory_schedule_activity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  UPDATE object_inventory_schedule SET mutated_at = '-infinity', active_since = '-infinity' WHERE bucket_id = OLD.bucket_id;
  RETURN OLD;
 END IF;
 UPDATE object_inventory_schedule SET mutated_at = NEW.mutated_at, active_since = NEW.active_since WHERE bucket_id = NEW.bucket_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_inventory_schedule_copy ON object_bucket_activity;
    CREATE TRIGGER object_inventory_schedule_copy AFTER INSERT OR DELETE OR UPDATE OF mutated_at, active_since
        ON object_bucket_activity FOR EACH ROW EXECUTE FUNCTION object_inventory_schedule_activity();
END
$$;
-- +goose StatementEnd

-- Copy active_since in batches. It only ever moves forward, so GREATEST
-- keeps a newer value a trigger wrote while a batch ran.
-- +goose StatementBegin
DO $backfill$
DECLARE copied bigint;
BEGIN
 LOOP
  UPDATE object_inventory_schedule s SET active_since = GREATEST(s.active_since, a.active_since)
  FROM object_bucket_activity a
  WHERE a.bucket_id = s.bucket_id AND s.bucket_id IN (
   SELECT s2.bucket_id FROM object_inventory_schedule s2 JOIN object_bucket_activity a2 ON a2.bucket_id = s2.bucket_id
   WHERE s2.active_since < a2.active_since ORDER BY s2.bucket_id LIMIT 1000);
  GET DIAGNOSTICS copied = ROW_COUNT;
  COMMIT;
  EXIT WHEN copied = 0;
 END LOOP;
END $backfill$;
-- +goose StatementEnd

-- A cancelled concurrent build leaves an invalid index that IF NOT EXISTS
-- would keep; drop any such leftover so a rerun builds it again.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('object_inventory_schedule_active_observed') AND NOT indisvalid) THEN
  DROP INDEX object_inventory_schedule_active_observed;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('object_inventory_schedule_activated') AND NOT indisvalid) THEN
  DROP INDEX object_inventory_schedule_activated;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('object_storage_bucket_usage_leased') AND NOT indisvalid) THEN
  DROP INDEX object_storage_bucket_usage_leased;
 END IF;
END $$;
-- +goose StatementEnd

-- Active buckets by observation: the oldest active observation.
CREATE INDEX CONCURRENTLY IF NOT EXISTS object_inventory_schedule_active_observed
    ON object_inventory_schedule (observed_at, bucket_id) WHERE mutated_at >= quiet_after;

-- Buckets whose window starts when they turned active, by that time.
CREATE INDEX CONCURRENTLY IF NOT EXISTS object_inventory_schedule_activated
    ON object_inventory_schedule (active_since, bucket_id) WHERE quiet_after > '-infinity'::timestamptz AND active_since > observed_at;

-- Usage rows holding a claim token: the buckets a sweep may have leased.
CREATE INDEX CONCURRENTLY IF NOT EXISTS object_storage_bucket_usage_leased
    ON object_storage_bucket_usage (bucket_id) WHERE token <> '';

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS object_storage_bucket_usage_leased;
DROP INDEX CONCURRENTLY IF EXISTS object_inventory_schedule_activated;
DROP INDEX CONCURRENTLY IF EXISTS object_inventory_schedule_active_observed;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_inventory_schedule_activity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  UPDATE object_inventory_schedule SET mutated_at = '-infinity' WHERE bucket_id = OLD.bucket_id;
  RETURN OLD;
 END IF;
 UPDATE object_inventory_schedule SET mutated_at = NEW.mutated_at WHERE bucket_id = NEW.bucket_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_inventory_schedule_copy ON object_bucket_activity;
    CREATE TRIGGER object_inventory_schedule_copy AFTER INSERT OR DELETE OR UPDATE OF mutated_at
        ON object_bucket_activity FOR EACH ROW EXECUTE FUNCTION object_inventory_schedule_activity();
END
$$;
-- +goose StatementEnd
ALTER TABLE object_inventory_schedule DROP COLUMN IF EXISTS active_since;
