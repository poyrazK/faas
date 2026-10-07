-- +goose Up
-- ADR-590: preserve intended PUT size and signed hash; observe validated body
-- identity without inventing provider ownership or draining unknown writes.
ALTER TABLE object_multipart_part_writers
 ADD COLUMN put_intent jsonb CHECK(put_intent IS NULL OR (dispatched AND copy_intent IS NULL AND COALESCE(put_intent->'schema'='1'::jsonb,false))),
 ADD COLUMN body_sha256 text NOT NULL DEFAULT '' CHECK(body_sha256='' OR (put_intent IS NOT NULL AND body_sha256 ~ '^[0-9a-f]{64}$'));
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
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (OLD.copy_intent IS NULL AND OLD.put_intent IS NULL AND OLD.body_sha256='' AND NEW.body_sha256='' AND NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.body_sha256=OLD.body_sha256 AND NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
  OR (OLD.dispatched AND NOT OLD.settled AND NEW.dispatched AND NOT NEW.settled AND OLD.put_intent IS NOT NULL AND OLD.copy_intent IS NULL
   AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent
   AND (OLD.body_sha256='' OR OLD.body_sha256=NEW.body_sha256) AND NEW.body_sha256 ~ '^[0-9a-f]{64}$' AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token
   AND (OLD.put_intent->>'expected_sha256'='' OR OLD.put_intent->>'expected_sha256'=NEW.body_sha256))
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
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.put_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent IS NULL AND g.source_bucket_id IS NULL
  AND NEW.put_intent->>'destination_key'=u.object_key AND NEW.put_intent->>'provider_upload_id'=u.provider_upload_id
  AND ((g.url_credential_id IS NULL AND (NEW.put_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes)
   OR (g.url_credential_id IS NOT NULL AND u.part_count>0 AND u.part_size_bytes>0 AND u.size_bytes>0
    AND NEW.part_number BETWEEN 1 AND u.part_count
    AND (NEW.put_intent->>'expected_size')::bigint=least(u.part_size_bytes,u.size_bytes-(NEW.part_number-1)::bigint*u.part_size_bytes)))
  AND (NEW.put_intent->>'expected_sha256'='' OR NEW.put_intent->>'expected_sha256' ~ '^[0-9a-f]{64}$')
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='PUT intent requires its original destination and admitted size';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE put_intent IS NOT NULL AND dispatched AND NOT settled) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Drain PUT intents and release capture holds before downgrade';
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
ALTER TABLE object_multipart_part_writers DROP COLUMN body_sha256,DROP COLUMN put_intent;
