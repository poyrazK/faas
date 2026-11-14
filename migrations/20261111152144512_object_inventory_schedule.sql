-- filename: 20261111152144512_object_inventory_schedule.sql
-- migration-compat: expand
-- Each trigger is created in its own statement (one transaction, one table
-- lock). Re-runnable after a partial run. The fill runs after the triggers,
-- so every write before it is covered by what it reads and every write
-- after it by a trigger.
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261111152144: the inventory sweep reads its due buckets from an
-- indexed schedule instead of every ready bucket.
--
-- One row per bucket that is not deleted, holding copies of what the due
-- rules (ADR-20261110151628) sort and split on:
--   attempt_at   object_storage_bucket_usage.attempt_at
--   observed_at  object_storage_bucket_usage.observed_at
--   quiet_after  observed_at - 30 s for a current-scope observation
--   mutated_at   object_bucket_activity.mutated_at
-- each -infinity when its source is missing. A bucket is quiet exactly when
-- mutated_at < quiet_after: never observed, all-versions scope and never
-- mutated all follow from the -infinity defaults. 30 s is
-- api.ObjectInventoryMutationStampGranularity, a literal here as in
-- object_bucket_mark_mutated.
--
-- Triggers write each copy in the transaction that changes its source
-- (a deleted source row resets its copies), so a copy is never behind what a
-- reader of the source sees. A stamp writes
-- the schedule row after its activity row; a usage write after its usage
-- row.
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    CREATE TABLE IF NOT EXISTS object_inventory_schedule (
        bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
        attempt_at timestamptz NOT NULL DEFAULT '-infinity',
        observed_at timestamptz NOT NULL DEFAULT '-infinity',
        quiet_after timestamptz NOT NULL DEFAULT '-infinity',
        mutated_at timestamptz NOT NULL DEFAULT '-infinity',
        CHECK (quiet_after <= observed_at)
    );
    -- Every bucket by attempt: the 5-minute retry with quiet buckets off.
    CREATE INDEX IF NOT EXISTS object_inventory_schedule_attempt
        ON object_inventory_schedule (attempt_at, bucket_id);
    -- Active buckets by attempt, and quiet buckets by observation, with
    -- quiet buckets on.
    CREATE INDEX IF NOT EXISTS object_inventory_schedule_active
        ON object_inventory_schedule (attempt_at, bucket_id) WHERE mutated_at >= quiet_after;
    CREATE INDEX IF NOT EXISTS object_inventory_schedule_quiet
        ON object_inventory_schedule (observed_at, bucket_id) WHERE mutated_at < quiet_after;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_inventory_schedule_bucket() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.state = 'deleted' THEN
  DELETE FROM object_inventory_schedule WHERE bucket_id = NEW.id;
  RETURN NEW;
 END IF;
 -- A new bucket has no usage or activity yet. A bucket that leaves deleted
 -- is taken as mutated now, like the fill below.
 INSERT INTO object_inventory_schedule (bucket_id, mutated_at)
 VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN '-infinity'::timestamptz ELSE now() END)
 ON CONFLICT (bucket_id) DO NOTHING;
 IF FOUND AND TG_OP = 'UPDATE' THEN
  UPDATE object_inventory_schedule s SET attempt_at = COALESCE(u.attempt_at, '-infinity'),
   observed_at = COALESCE(u.observed_at, '-infinity'),
   quiet_after = CASE WHEN u.observed_at IS NOT NULL AND u.inventory_scope = 'current'
    THEN u.observed_at - interval '30 seconds' ELSE '-infinity' END
  FROM object_storage_bucket_usage u WHERE u.bucket_id = NEW.id AND s.bucket_id = NEW.id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_inventory_schedule_usage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  UPDATE object_inventory_schedule SET attempt_at = '-infinity', observed_at = '-infinity', quiet_after = '-infinity'
  WHERE bucket_id = OLD.bucket_id;
  RETURN OLD;
 END IF;
 UPDATE object_inventory_schedule SET attempt_at = COALESCE(NEW.attempt_at, '-infinity'),
  observed_at = COALESCE(NEW.observed_at, '-infinity'),
  quiet_after = CASE WHEN NEW.observed_at IS NOT NULL AND NEW.inventory_scope = 'current'
   THEN NEW.observed_at - interval '30 seconds' ELSE '-infinity' END
 WHERE bucket_id = NEW.bucket_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd

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
    DROP TRIGGER IF EXISTS object_inventory_schedule_insert ON object_buckets;
    CREATE TRIGGER object_inventory_schedule_insert AFTER INSERT ON object_buckets
        FOR EACH ROW EXECUTE FUNCTION object_inventory_schedule_bucket();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_inventory_schedule_state ON object_buckets;
    CREATE TRIGGER object_inventory_schedule_state AFTER UPDATE OF state ON object_buckets
        FOR EACH ROW WHEN ((OLD.state = 'deleted') IS DISTINCT FROM (NEW.state = 'deleted'))
        EXECUTE FUNCTION object_inventory_schedule_bucket();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_inventory_schedule_copy ON object_storage_bucket_usage;
    CREATE TRIGGER object_inventory_schedule_copy AFTER INSERT OR DELETE OR UPDATE OF attempt_at, observed_at, inventory_scope
        ON object_storage_bucket_usage FOR EACH ROW EXECUTE FUNCTION object_inventory_schedule_usage();
END
$$;
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

-- Unfinished listings with a page are due at once whatever their attempt
-- time; the due query adds them from here.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'object_storage_inventory_scans_open' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX object_storage_inventory_scans_open';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS object_storage_inventory_scans_open
    ON object_storage_inventory_scans (started_at) WHERE pages > 0 AND NOT complete;

-- Fill: every bucket that is not deleted, taken as mutated now (each is
-- active until its next listing, as the activity fill did), then its usage.
-- A row a trigger wrote first keeps its copies and gains the newer stamp.
INSERT INTO object_inventory_schedule (bucket_id, mutated_at)
SELECT id, now() FROM object_buckets WHERE state <> 'deleted'
ON CONFLICT (bucket_id) DO UPDATE SET mutated_at = GREATEST(object_inventory_schedule.mutated_at, EXCLUDED.mutated_at);

-- A usage write racing this copy may leave an older attempt or observation,
-- which only makes the bucket due or active sooner; its next claim or
-- listing rewrites it.
UPDATE object_inventory_schedule s SET attempt_at = GREATEST(s.attempt_at, COALESCE(u.attempt_at, '-infinity')),
 observed_at = GREATEST(s.observed_at, COALESCE(u.observed_at, '-infinity')),
 quiet_after = CASE WHEN u.observed_at IS NOT NULL AND u.inventory_scope = 'current'
  THEN GREATEST(s.observed_at, u.observed_at) - interval '30 seconds' ELSE '-infinity' END
FROM object_storage_bucket_usage u WHERE u.bucket_id = s.bucket_id;

-- +goose Down
SET lock_timeout = '5s';
DROP TRIGGER IF EXISTS object_inventory_schedule_copy ON object_bucket_activity;
DROP TRIGGER IF EXISTS object_inventory_schedule_copy ON object_storage_bucket_usage;
DROP TRIGGER IF EXISTS object_inventory_schedule_state ON object_buckets;
DROP TRIGGER IF EXISTS object_inventory_schedule_insert ON object_buckets;
DROP INDEX CONCURRENTLY IF EXISTS object_storage_inventory_scans_open;
DROP FUNCTION IF EXISTS object_inventory_schedule_activity();
DROP FUNCTION IF EXISTS object_inventory_schedule_usage();
DROP FUNCTION IF EXISTS object_inventory_schedule_bucket();
DROP TABLE IF EXISTS object_inventory_schedule;
