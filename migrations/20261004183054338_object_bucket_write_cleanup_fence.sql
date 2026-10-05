-- filename: 20261004183054338_object_bucket_write_cleanup_fence.sql

-- +goose Up
-- Retain accepted write evidence before any bucket cleanup or metadata cascade.
-- Admission's SHARE lock serializes older replicas with deletion's row update.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_bucket_pending_writes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid; bucket_state text;
BEGIN
 IF TG_TABLE_NAME='object_storage_write_admissions' THEN
  SELECT state INTO bucket_state FROM object_buckets WHERE id=NEW.bucket_id FOR SHARE;
  IF bucket_state IS DISTINCT FROM 'ready' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_pending_write_fenced',MESSAGE='Bucket cleanup fences new write admission';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN bid:=OLD.id;
 ELSE
  IF NEW.state NOT IN ('deleting','deleted') THEN RETURN NEW; END IF;
  bid:=NEW.id;
 END IF;
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions w WHERE w.bucket_id=bid AND w.state='pending'
  AND (w.multipart_upload_id IS NULL OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
   WHERE m.id=w.multipart_upload_id AND m.state IN ('initiating','active','completing','completing_conditional','aborting')))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_pending_write_fenced',MESSAGE='Settle accepted writes before bucket cleanup';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_bucket_pending_write_fence ON object_buckets;
CREATE TRIGGER object_bucket_pending_write_fence BEFORE UPDATE OR DELETE ON object_buckets
 FOR EACH ROW EXECUTE FUNCTION fence_object_bucket_pending_writes();
DROP TRIGGER IF EXISTS object_write_bucket_cleanup_fence ON object_storage_write_admissions;
CREATE TRIGGER object_write_bucket_cleanup_fence BEFORE INSERT ON object_storage_write_admissions
 FOR EACH ROW EXECUTE FUNCTION fence_object_bucket_pending_writes();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions WHERE state='pending') OR EXISTS(SELECT 1 FROM object_buckets WHERE state='deleting') THEN
  RAISE EXCEPTION 'Settle writes and bucket cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_write_bucket_cleanup_fence ON object_storage_write_admissions;
DROP TRIGGER object_bucket_pending_write_fence ON object_buckets;
DROP FUNCTION fence_object_bucket_pending_writes();
