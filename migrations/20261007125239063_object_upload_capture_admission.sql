-- +goose Up
-- ADR-590: upload receipts and multipart sessions use their original durable journal
-- as writer evidence. Count pending receipts and all live sessions until recovery settles
-- them; a capture hold closes only new admission, never original settlement.
-- Enforce admission for replicas predating the Go-side check too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_upload_capture_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bucket_state text;
BEGIN
 -- Fence queries need a fresh snapshot after waiting for the source lock.
 -- Reject old transaction snapshots rather than hiding a committed hold.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_admission_isolation',MESSAGE='Upload admission requires READ COMMITTED';
 END IF;
 -- Match the application admission order: account before source bucket.
 -- An older replica's INSERT must not invert the account FK/row locks.
 PERFORM id FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 SELECT state INTO bucket_state FROM object_buckets
 WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id FOR UPDATE;
 IF bucket_state IS DISTINCT FROM 'ready' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_bucket_not_ready',MESSAGE='Bucket cleanup fences upload admission';
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=NEW.bucket_id) THEN
  RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_upload_capture_fenced',MESSAGE='Checkpoint capture fences new upload admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_upload_capture_admission ON object_upload_completions;
CREATE TRIGGER object_upload_capture_admission BEFORE INSERT ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION fence_object_upload_capture_admission();
DROP TRIGGER IF EXISTS object_multipart_capture_admission ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_capture_admission BEFORE INSERT ON object_storage_multipart_uploads
 FOR EACH ROW EXECUTE FUNCTION fence_object_upload_capture_admission();
CREATE INDEX IF NOT EXISTS object_upload_capture_pending_idx ON object_upload_completions(bucket_id) WHERE status='pending' OR (write_phase='untracked' AND status='failed');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_capture_admission_down_held',MESSAGE='Release capture holds before removing upload admission enforcement';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_upload_capture_admission ON object_upload_completions;
DROP TRIGGER object_multipart_capture_admission ON object_storage_multipart_uploads;
DROP INDEX object_upload_capture_pending_idx;
DROP FUNCTION fence_object_upload_capture_admission();
