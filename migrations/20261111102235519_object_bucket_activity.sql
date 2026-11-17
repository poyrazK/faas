-- filename: 20261111102235519_object_bucket_activity.sql
-- migration-compat: expand
-- Each trigger is created in its own statement (one transaction, one table
-- lock). Re-runnable after a partial run. The backfill runs last, so every
-- mutation before it is covered by its timestamp and every one after it by a
-- trigger.
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261110151628 step 3: when each bucket's contents last changed.
--
-- mutated_at is the last time an admitted mutation may have changed what a
-- bucket holds, stamped in the transaction that records it:
--   - a source writer begins or ends (object_bucket_mutations, ADR-590: every
--     provider write, copy, multipart step, upload route and clone grant
--     journals itself there before provider IO and clears it after a
--     validated acknowledgement);
--   - a deletion journal row is created or changes state (object_deletions:
--     single, bulk, version and lifecycle expiry);
--   - versioning, Object Lock or capacity reconciliation changes.
-- A bucket whose mutated_at is older than the start of its published listing
-- (less the stamp granularity) holds what that listing counted: it is quiet.
--
-- Stamps are coarse: a bucket's row is rewritten at most once per 30 s
-- (api.ObjectInventoryMutationStampGranularity), so a burst of writes costs
-- one row update, not one per request. Readers allow for that by calling a
-- bucket quiet only when mutated_at is older than the listing's start by more
-- than the granularity.
--
-- active_since is the first stamp after the bucket was last quiet: admission
-- anchors an active bucket's freshness there once quiet buckets keep their
-- inventory for a day (step 4).
--
-- The table has no foreign key: inserting a row would otherwise take a key
-- share lock on the bucket inside every first stamp. A hard-deleted bucket
-- leaves one orphan row.
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS object_bucket_activity (
    bucket_id uuid PRIMARY KEY,
    mutated_at timestamptz NOT NULL,
    active_since timestamptz NOT NULL,
    CHECK (active_since <= mutated_at)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_bucket_mark_mutated(target uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
 observed timestamptz;
BEGIN
 IF target IS NULL THEN
  RETURN;
 END IF;
 SELECT observed_at INTO observed FROM object_storage_bucket_usage WHERE bucket_id=target;
 -- A plain UPDATE whose WHERE fails takes no row lock, so a request within
 -- the granularity of the last stamp reads the row and moves on.
 UPDATE object_bucket_activity a SET mutated_at=now(),
  active_since=CASE WHEN a.mutated_at < COALESCE(observed,'-infinity'::timestamptz) - interval '30 seconds' THEN now() ELSE a.active_since END
 WHERE a.bucket_id=target AND a.mutated_at < now() - interval '30 seconds';
 IF NOT FOUND THEN
  INSERT INTO object_bucket_activity(bucket_id,mutated_at,active_since) VALUES(target,now(),now())
  ON CONFLICT (bucket_id) DO NOTHING;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_bucket_activity_stamp() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM object_bucket_mark_mutated(OLD.bucket_id);
  RETURN OLD;
 END IF;
 PERFORM object_bucket_mark_mutated(NEW.bucket_id);
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_mutations;
    CREATE TRIGGER object_bucket_activity_stamp AFTER INSERT OR DELETE ON object_bucket_mutations
        FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_deletions;
    CREATE TRIGGER object_bucket_activity_stamp AFTER INSERT OR UPDATE OF state ON object_deletions
        FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_versioning;
    CREATE TRIGGER object_bucket_activity_stamp AFTER INSERT OR UPDATE ON object_bucket_versioning
        FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_object_lock;
    CREATE TRIGGER object_bucket_activity_stamp AFTER INSERT OR UPDATE ON object_bucket_object_lock
        FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp();
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_storage_capacity_reconciliations;
    CREATE TRIGGER object_bucket_activity_stamp AFTER INSERT OR UPDATE OF state ON object_storage_capacity_reconciliations
        FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp();
END
$$;
-- +goose StatementEnd

-- Every bucket that exists now may have changed before the triggers above
-- existed, so its activity starts here. A bucket created later has no row
-- until its first mutation, and none is needed: it has had none.
INSERT INTO object_bucket_activity(bucket_id,mutated_at,active_since)
SELECT id, now(), now() FROM object_buckets
ON CONFLICT (bucket_id) DO UPDATE SET mutated_at=GREATEST(object_bucket_activity.mutated_at, EXCLUDED.mutated_at);

-- +goose Down
SET lock_timeout = '5s';
DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_storage_capacity_reconciliations;
DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_object_lock;
DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_versioning;
DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_deletions;
DROP TRIGGER IF EXISTS object_bucket_activity_stamp ON object_bucket_mutations;
DROP FUNCTION IF EXISTS object_bucket_activity_stamp();
DROP FUNCTION IF EXISTS object_bucket_mark_mutated(uuid);
DROP TABLE IF EXISTS object_bucket_activity;
