-- filename: 20261004183223121_object_write_proof_custody.sql

-- +goose Up
-- Account-before-bucket locking matches all write admission transactions.
-- Older replicas must also retain a pending receipt's exact object key.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_write_key() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner uuid; own_write uuid;
BEGIN
 SELECT account_id INTO owner FROM object_buckets WHERE id=NEW.bucket_id;
 PERFORM 1 FROM accounts WHERE id=owner FOR UPDATE;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR SHARE;
 IF TG_TABLE_NAME='object_storage_write_admissions' THEN
  own_write:=NEW.id;
 ELSE
  own_write:=NEW.last_write_id;
  IF TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id THEN own_write:=NULL; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions w
  WHERE w.bucket_id=NEW.bucket_id AND w.key_hash=NEW.key_hash AND w.state='pending'
  AND w.id IS DISTINCT FROM own_write
  AND (w.multipart_upload_id IS NULL OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
   WHERE m.id=w.multipart_upload_id AND m.state IN ('initiating','active','completing','completing_conditional','aborting')))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_write_key_fenced',MESSAGE='Settle the pending write before replacing its proof';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_aaa_write_key_fence ON object_storage_write_admissions;
CREATE TRIGGER object_aaa_write_key_fence BEFORE INSERT ON object_storage_write_admissions
 FOR EACH ROW EXECUTE FUNCTION fence_object_write_key();
DROP TRIGGER IF EXISTS object_grant_write_key_fence ON object_storage_key_grants;
CREATE TRIGGER object_grant_write_key_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants
 FOR EACH ROW EXECUTE FUNCTION fence_object_write_key();

CREATE INDEX IF NOT EXISTS object_write_pending_key_idx ON object_storage_write_admissions(bucket_id,key_hash) WHERE state='pending';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions WHERE state='pending') THEN
  RAISE EXCEPTION 'Settle pending writes before removing proof custody';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX object_write_pending_key_idx;
DROP TRIGGER object_grant_write_key_fence ON object_storage_key_grants;
DROP TRIGGER object_aaa_write_key_fence ON object_storage_write_admissions;
DROP FUNCTION fence_object_write_key();
