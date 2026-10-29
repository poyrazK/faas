-- migration-compat: contract
-- filename: 20261025120158599_object_deletion_per_key_fences.sql
--
-- ADR-20261025115958: a bucket holds one active deletion per key, not one per
-- bucket, and only a deletion of the same key fences a write admission, a
-- multipart upload or a key grant. Bucket-wide work (versioning, capacity
-- reconciliation, inventory, bucket deletion) still waits for every active
-- deletion. The previous release still counts every active deletion in the
-- bucket as pending before it admits one, so it stays serialized beside this,
-- but migration-lint counts dropping the per-bucket index as a contract
-- statement, so a full release applies this. The new index is built
-- concurrently before the old one is dropped; every step is guarded, so a
-- rerun converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'object_deletions_active_key' AND NOT i.indisvalid) THEN
  -- A function cannot drop concurrently; an invalid index is not used.
  EXECUTE 'DROP INDEX object_deletions_active_key';
 END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS object_deletions_active_key
    ON object_deletions (bucket_id, object_key) WHERE state IN ('prepared','dispatched');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_deletion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bid uuid; fence_key text; fence_key_hash text;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF TG_OP='DELETE' THEN bid:=OLD.id;
  ELSE IF NEW.state=OLD.state OR NEW.state NOT IN ('deleting','deleted') THEN RETURN NEW; END IF; bid:=NEW.id; END IF;
 ELSE
  bid:=NEW.bucket_id;
  IF TG_TABLE_NAME='object_bucket_versioning' THEN IF NEW.state='ready' THEN RETURN NEW; END IF; END IF;
  -- Writes, multipart uploads and key grants touch one key.
  IF TG_TABLE_NAME IN ('object_storage_write_admissions','object_storage_key_grants') THEN fence_key_hash:=NEW.key_hash;
  ELSIF TG_TABLE_NAME='object_storage_multipart_uploads' THEN fence_key:=NEW.object_key;
  END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_deletions d WHERE d.bucket_id=bid AND d.state IN ('prepared','dispatched')
  AND (fence_key IS NULL OR d.object_key=fence_key)
  AND (fence_key_hash IS NULL OR encode(sha256(convert_to(d.object_key,'UTF8')),'hex')=fence_key_hash)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_fenced',MESSAGE='Unsettled deletion fences mutation and inventory';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP INDEX CONCURRENTLY IF EXISTS object_deletions_active_bucket;

-- +goose Down
SET lock_timeout = '5s';

-- Fails while two deletions in one bucket are active; let them settle first.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS object_deletions_active_bucket
    ON object_deletions (bucket_id) WHERE state IN ('prepared','dispatched');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_deletion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF TG_OP='DELETE' THEN bid:=OLD.id;
  ELSE IF NEW.state=OLD.state OR NEW.state NOT IN ('deleting','deleted') THEN RETURN NEW; END IF; bid:=NEW.id; END IF;
 ELSE
  bid:=NEW.bucket_id;
  IF TG_TABLE_NAME='object_bucket_versioning' THEN IF NEW.state='ready' THEN RETURN NEW; END IF; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_deletions d WHERE d.bucket_id=bid AND d.state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_fenced',MESSAGE='Unsettled deletion fences mutation and inventory';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP INDEX CONCURRENTLY IF EXISTS object_deletions_active_key;
