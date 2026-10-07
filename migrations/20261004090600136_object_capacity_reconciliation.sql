-- +goose Up
CREATE TABLE IF NOT EXISTS object_storage_write_admissions (
 id uuid PRIMARY KEY, bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 key_hash text NOT NULL CHECK (length(key_hash)=64),
 kind text NOT NULL CHECK (kind IN ('proxy','multipart')),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','settled')),
 multipart_upload_id uuid REFERENCES object_storage_multipart_uploads(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(), settled_at timestamptz,
 CHECK ((kind='multipart')=(multipart_upload_id IS NOT NULL)),
 CHECK ((state='settled')=(settled_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS object_write_admissions_bucket_idx ON object_storage_write_admissions(bucket_id);
ALTER TABLE object_storage_key_grants ADD COLUMN IF NOT EXISTS reclaimable boolean NOT NULL DEFAULT false;
ALTER TABLE object_storage_key_grants ADD COLUMN IF NOT EXISTS last_write_id uuid REFERENCES object_storage_write_admissions(id);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_key_grants'::regclass AND conname='object_grant_tracked') THEN
  ALTER TABLE object_storage_key_grants ADD CONSTRAINT object_grant_tracked CHECK (NOT reclaimable OR last_write_id IS NOT NULL);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS object_storage_capacity_reconciliations (
 id uuid PRIMARY KEY, bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 state text NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting','scanning','completed','cancelled','blocked','failed')),
 lease_token text NOT NULL DEFAULT '', lease_until timestamptz, retry_at timestamptz NOT NULL DEFAULT now(), deadline_at timestamptz NOT NULL,
 before_bytes bigint NOT NULL DEFAULT 0 CHECK (before_bytes>=0), before_keys bigint NOT NULL DEFAULT 0 CHECK (before_keys>=0),
 after_bytes bigint NOT NULL DEFAULT 0 CHECK (after_bytes>=0), after_keys bigint NOT NULL DEFAULT 0 CHECK (after_keys>=0),
 reclaimed_bytes bigint NOT NULL DEFAULT 0 CHECK (reclaimed_bytes>=0), reclaimed_keys bigint NOT NULL DEFAULT 0 CHECK (reclaimed_keys>=0),
 pending_writes bigint NOT NULL DEFAULT 0 CHECK (pending_writes>=0),
 last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('','untracked_writes','unsettled_writes','multipart_active','deadline','inventory_failed')),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 CHECK ((lease_token='')=(lease_until IS NULL)),
 CHECK ((state='scanning')=(lease_until IS NOT NULL)),
 CHECK ((state IN ('completed','cancelled','blocked','failed'))=(finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS object_capacity_active_bucket_idx ON object_storage_capacity_reconciliations(bucket_id) WHERE state IN ('waiting','scanning');
CREATE INDEX IF NOT EXISTS object_capacity_due_idx ON object_storage_capacity_reconciliations(retry_at,id) WHERE state IN ('waiting','scanning');
-- Older apid replicas must neither bypass the write fence nor upgrade a legacy grant.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_capacity_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF NEW.state<>'deleting' OR OLD.state='deleting' THEN RETURN NEW; END IF;
  bid:=NEW.id;
 ELSE bid:=NEW.bucket_id;
 END IF;
 IF EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_capacity_write_fenced',MESSAGE='Object capacity reconciliation fences new writes';
 END IF;
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NULL OR (TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id) THEN
   NEW.reclaimable:=false; NEW.last_write_id:=NULL;
  ELSE
   IF NOT EXISTS (SELECT 1 FROM object_storage_write_admissions WHERE id=NEW.last_write_id AND bucket_id=bid AND key_hash=NEW.key_hash AND state='pending') THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object grant lacks matching write admission';
   END IF;
   IF TG_OP='UPDATE' THEN NEW.reclaimable:=OLD.reclaimable AND NEW.reclaimable; END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_grant_capacity_fence ON object_storage_key_grants;
CREATE TRIGGER object_grant_capacity_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants FOR EACH ROW EXECUTE FUNCTION fence_object_capacity_write();
DROP TRIGGER IF EXISTS object_multipart_capacity_fence ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_capacity_fence BEFORE INSERT ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION fence_object_capacity_write();
DROP TRIGGER IF EXISTS object_bucket_capacity_fence ON object_buckets;
CREATE TRIGGER object_bucket_capacity_fence BEFORE UPDATE ON object_buckets FOR EACH ROW EXECUTE FUNCTION fence_object_capacity_write();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations WHERE state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION 'Cancel or settle capacity reconciliations before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_grant_capacity_fence ON object_storage_key_grants;
DROP TRIGGER object_multipart_capacity_fence ON object_storage_multipart_uploads;
DROP TRIGGER object_bucket_capacity_fence ON object_buckets;
DROP FUNCTION fence_object_capacity_write();
DROP TABLE object_storage_capacity_reconciliations;
ALTER TABLE object_storage_key_grants DROP CONSTRAINT object_grant_tracked, DROP COLUMN reclaimable, DROP COLUMN last_write_id;
DROP TABLE object_storage_write_admissions;
