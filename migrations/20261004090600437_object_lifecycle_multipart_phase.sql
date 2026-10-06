-- filename: 20261004090600437_object_lifecycle_multipart_phase.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_lifecycle_scans ADD COLUMN IF NOT EXISTS phase text NOT NULL DEFAULT 'objects' CHECK(phase IN ('objects','multipart'));
ALTER TABLE object_lifecycle_scans ADD COLUMN IF NOT EXISTS last_upload_id uuid;
ALTER TABLE object_lifecycle_scans ADD COLUMN IF NOT EXISTS scanned_uploads bigint NOT NULL DEFAULT 0 CHECK(scanned_uploads>=0);
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_lifecycle_scans'::regclass AND conname='object_lifecycle_multipart_progress') THEN
  ALTER TABLE object_lifecycle_scans ADD CONSTRAINT object_lifecycle_multipart_progress CHECK((last_upload_id IS NULL)=(scanned_uploads=0) AND (phase='multipart' OR scanned_uploads=0));
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS object_lifecycle_multipart_discovery ON object_storage_multipart_uploads(bucket_id,id) WHERE state='active';

CREATE OR REPLACE FUNCTION protect_object_lifecycle_scan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.revision<>OLD.revision
  OR NEW.rules<>OLD.rules OR NEW.created_at<>OLD.created_at
  OR NEW.scanned_keys<OLD.scanned_keys OR NEW.scanned_keys>OLD.scanned_keys+1
  OR (NEW.last_key IS DISTINCT FROM OLD.last_key AND (OLD.phase<>'objects' OR NEW.last_key COLLATE "C"<=OLD.last_key COLLATE "C" OR NEW.scanned_keys<>OLD.scanned_keys+1))
  OR (NEW.last_key=OLD.last_key AND NEW.scanned_keys<>OLD.scanned_keys)
  OR NEW.scanned_uploads<OLD.scanned_uploads OR NEW.scanned_uploads>OLD.scanned_uploads+1
  OR (NEW.last_upload_id IS DISTINCT FROM OLD.last_upload_id AND (OLD.phase<>'multipart' OR NEW.last_upload_id IS NULL OR NEW.last_upload_id<=OLD.last_upload_id OR NEW.scanned_uploads<>OLD.scanned_uploads+1))
  OR (NEW.last_upload_id IS NOT DISTINCT FROM OLD.last_upload_id AND NEW.scanned_uploads<>OLD.scanned_uploads)
  OR (NEW.phase<>OLD.phase AND (OLD.phase<>'objects' OR NEW.phase<>'multipart'
   OR NEW.last_key<>OLD.last_key OR NEW.scanned_keys<>OLD.scanned_keys OR NEW.scanned_uploads<>OLD.scanned_uploads
   OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(OLD.rules) r WHERE r->>'status'='Enabled' AND r ? 'abort_incomplete_multipart_days')))
  OR (OLD.state<>'scanning' AND NEW IS DISTINCT FROM OLD) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle scan identity and progress cannot be rewritten';
 END IF;
 RETURN NEW;
END $$;

ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS lifecycle_scan_id uuid REFERENCES object_lifecycle_scans(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS lifecycle_binding jsonb NOT NULL DEFAULT '{}';
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_multipart_uploads'::regclass AND conname='object_multipart_lifecycle_binding') THEN
  ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_lifecycle_binding CHECK(
 (lifecycle_scan_id IS NULL AND lifecycle_binding='{}') OR
 (lifecycle_scan_id IS NOT NULL AND jsonb_typeof(lifecycle_binding)='object'
  AND lifecycle_binding ?& ARRAY['scan_id','scan_token','rule_id','expected_provider_upload_id','expected_created_at']
  AND lifecycle_binding - ARRAY['scan_id','scan_token','rule_id','expected_provider_upload_id','expected_created_at']='{}'
  AND jsonb_typeof(lifecycle_binding->'scan_id')='string' AND lifecycle_binding->>'scan_id'=lifecycle_scan_id::text
  AND jsonb_typeof(lifecycle_binding->'scan_token')='string' AND octet_length(lifecycle_binding->>'scan_token') BETWEEN 1 AND 128
  AND jsonb_typeof(lifecycle_binding->'rule_id')='string' AND char_length(lifecycle_binding->>'rule_id') BETWEEN 1 AND 255
  AND jsonb_typeof(lifecycle_binding->'expected_provider_upload_id')='string' AND lifecycle_binding->>'expected_provider_upload_id'=provider_upload_id AND provider_upload_id<>''
  AND jsonb_typeof(lifecycle_binding->'expected_created_at')='string' AND lifecycle_binding->>'expected_created_at'<>''
  AND octet_length(lifecycle_binding::text)<=8192 AND state IN ('aborting','aborted'))
);
 END IF;
END $$;

CREATE OR REPLACE FUNCTION fence_object_lifecycle_multipart() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.lifecycle_scan_id IS NOT NULL OR NEW.lifecycle_binding<>'{}' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an existing active upload';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.lifecycle_scan_id IS NOT NULL THEN
  IF NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding
   OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id
   OR NEW.object_key<>OLD.object_key OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission identity is immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF OLD.state<>'active' OR NEW.state<>'aborting' OR NEW.lease_until IS NOT NULL
  OR OLD.lease_until>clock_timestamp() OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at
  OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key
  OR (NEW.lifecycle_binding->>'expected_created_at')::timestamptz<>OLD.created_at THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires the unchanged active upload';
 END IF;
 -- The store takes these locks before the upload row. Direct SQL callers must
 -- follow the same bucket/account/upload ordering to avoid deadlocks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an owned ready bucket'; END IF;
 PERFORM 1 FROM accounts WHERE id=aid FOR UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning' AND s.phase='multipart'
   AND s.created_at>=NEW.created_at AND (s.last_upload_id IS NULL OR NEW.id>s.last_upload_id)
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND coalesce(r->'filter'->'tags','{}')='{}' AND (r->>'abort_incomplete_multipart_days')::bigint>0
   -- Compare elapsed UTC days instead of constructing an out-of-range date
   -- for an otherwise valid large days value.
   AND (clock_timestamp() AT TIME ZONE 'UTC')::date-(NEW.created_at AT TIME ZONE 'UTC')::date > (r->>'abort_incomplete_multipart_days')::bigint
 ) INTO allowed;
 IF NOT allowed THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_multipart_fenced',MESSAGE='A live matching lifecycle scan and aged upload are required before admission';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lifecycle_multipart_fence ON object_storage_multipart_uploads;
CREATE TRIGGER object_lifecycle_multipart_fence BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION fence_object_lifecycle_multipart();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE lifecycle_scan_id IS NOT NULL)
  OR EXISTS(SELECT 1 FROM object_lifecycle_scans WHERE phase<>'objects' OR scanned_uploads<>0) THEN
  RAISE EXCEPTION 'Cannot discard lifecycle multipart admissions or scan progress';
 END IF;
END $$;
DROP TRIGGER object_lifecycle_multipart_fence ON object_storage_multipart_uploads;
DROP FUNCTION fence_object_lifecycle_multipart();
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_lifecycle_binding;
ALTER TABLE object_storage_multipart_uploads DROP COLUMN lifecycle_binding;
ALTER TABLE object_storage_multipart_uploads DROP COLUMN lifecycle_scan_id;
DROP INDEX object_lifecycle_multipart_discovery;
ALTER TABLE object_lifecycle_scans DROP CONSTRAINT object_lifecycle_multipart_progress;
ALTER TABLE object_lifecycle_scans DROP COLUMN scanned_uploads;
ALTER TABLE object_lifecycle_scans DROP COLUMN last_upload_id;
ALTER TABLE object_lifecycle_scans DROP COLUMN phase;
CREATE OR REPLACE FUNCTION protect_object_lifecycle_scan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.revision<>OLD.revision
   OR NEW.rules<>OLD.rules OR NEW.created_at<>OLD.created_at
   OR NEW.scanned_keys<OLD.scanned_keys OR NEW.scanned_keys>OLD.scanned_keys+1
   OR (NEW.last_key IS DISTINCT FROM OLD.last_key AND (NEW.last_key COLLATE "C"<=OLD.last_key COLLATE "C" OR NEW.scanned_keys<>OLD.scanned_keys+1))
   OR (NEW.last_key=OLD.last_key AND NEW.scanned_keys<>OLD.scanned_keys)
   OR (OLD.state<>'scanning' AND NEW IS DISTINCT FROM OLD) THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle scan identity and progress cannot be rewritten';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
