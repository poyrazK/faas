-- +goose Up
-- ADR-590: copy intent is recorded only in the original once-only dispatch.
-- Existing uncertain attempts remain without intent; they cannot be adopted.
ALTER TABLE object_multipart_part_writers ADD COLUMN IF NOT EXISTS copy_intent jsonb
 CHECK(copy_intent IS NULL OR (dispatched AND COALESCE((jsonb_typeof(copy_intent)='object' AND copy_intent->>'schema'='1'),false)));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_object_multipart_part_writer() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE; g object_storage_multipart_part_grants%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.dispatched AND NOT OLD.settled THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain part evidence cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writers require READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
 PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 SELECT * INTO g FROM object_storage_multipart_part_grants WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number;
 IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id AND b.state='ready' AND b.backend_id=NEW.backend_id AND b.backend_fingerprint=NEW.backend_fingerprint AND b.physical_name=NEW.physical_name) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writer requires its original placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_part_capture_fenced',MESSAGE='Capture fences new part transfer admission';
  END IF;
  IF NOT NEW.managed OR NEW.dispatched OR NEW.settled OR u.state<>'active' OR g.transfer_token IS DISTINCT FROM NEW.transfer_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Reserve part evidence only with new unfenced transfer authority';
  END IF;
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled','copy_intent']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled','copy_intent']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (OLD.copy_intent IS NULL AND NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent AND NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Dispatch once; settle only the original attempt with qualified proof';
 END IF;
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.copy_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent->>'destination_key'=u.object_key AND NEW.copy_intent->>'provider_upload_id'=u.provider_upload_id
  AND (NEW.copy_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes
  AND (NEW.copy_intent->>'source_size')::bigint >= (NEW.copy_intent->>'expected_size')::bigint
  AND NEW.copy_intent->>'source_etag'<>'' AND NEW.copy_intent->>'source_key'<>''
  AND ((g.source_bucket_id IS NULL AND NEW.copy_intent->>'source_bucket_id'=u.bucket_id::text)
   OR (NEW.copy_intent->>'source_bucket_id'=g.source_bucket_id::text AND NEW.copy_intent->>'source_key'=g.source_key))
  AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id::text=NEW.copy_intent->>'source_bucket_id' AND b.account_id=u.account_id AND b.state='ready'
   AND b.backend_id=NEW.copy_intent->>'source_backend_id' AND b.backend_fingerprint=NEW.copy_intent->>'source_backend_fingerprint' AND b.physical_name=NEW.copy_intent->>'source_physical_name')
  AND (((NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint>=0 AND (NEW.copy_intent->>'range_last')::bigint<(NEW.copy_intent->>'source_size')::bigint
    AND (NEW.copy_intent->>'expected_size')::bigint=(NEW.copy_intent->>'range_last')::bigint-(NEW.copy_intent->>'range_first')::bigint+1)
   OR (NOT (NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint=0 AND (NEW.copy_intent->>'range_last')::bigint=0 AND NEW.copy_intent->>'expected_size'=NEW.copy_intent->>'source_size'))
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Copy intent requires the original measured source, range and destination';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE copy_intent IS NOT NULL AND dispatched AND NOT settled) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Drain copy intents and release capture holds before downgrade';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION guard_object_multipart_part_writer() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE; g object_storage_multipart_part_grants%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.dispatched AND NOT OLD.settled THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain part evidence cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writers require READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
 PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 SELECT * INTO g FROM object_storage_multipart_part_grants WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number;
 IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id AND b.state='ready' AND b.backend_id=NEW.backend_id AND b.backend_fingerprint=NEW.backend_fingerprint AND b.physical_name=NEW.physical_name) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writer requires its original placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_part_capture_fenced',MESSAGE='Capture fences new part transfer admission';
  END IF;
  IF NOT NEW.managed OR NEW.dispatched OR NEW.settled OR u.state<>'active' OR g.transfer_token IS DISTINCT FROM NEW.transfer_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Reserve part evidence only with new unfenced transfer authority';
  END IF;
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Dispatch once; settle only the original attempt with qualified proof';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE object_multipart_part_writers DROP COLUMN copy_intent;
