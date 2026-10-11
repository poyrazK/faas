-- +goose Up
-- ADR-590: lifecycle and customer deletions use their original durable journal
-- as writer evidence. Count prepared/dispatched intents until recovery settles
-- them; a capture hold closes only new admission, never original settlement.
-- Enforce admission for replicas predating the Go-side check too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_deletion_capture_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bucket_state text;
BEGIN
 -- Fence queries need a fresh snapshot after waiting for the source lock.
 -- Reject old transaction snapshots rather than hiding a committed hold.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_admission_isolation',MESSAGE='Deletion admission requires READ COMMITTED';
 END IF;
 SELECT state INTO bucket_state FROM object_buckets WHERE id=NEW.bucket_id FOR UPDATE;
 IF bucket_state IS DISTINCT FROM 'ready' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_bucket_not_ready',MESSAGE='Bucket cleanup fences deletion admission';
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=NEW.bucket_id) THEN
  RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_deletion_capture_fenced',MESSAGE='Checkpoint capture fences new deletion admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_deletion_capture_admission ON object_deletions;
CREATE TRIGGER object_deletion_capture_admission BEFORE INSERT ON object_deletions
 FOR EACH ROW EXECUTE FUNCTION fence_object_deletion_capture_admission();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_capture_admission_down_held',MESSAGE='Release capture holds before removing deletion admission enforcement';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_deletion_capture_admission ON object_deletions;
DROP FUNCTION fence_object_deletion_capture_admission();
