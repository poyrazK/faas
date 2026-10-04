-- filename: 20261004090600484_object_encryption_snapshots.sql

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_encryption_snapshot(e jsonb, owner uuid) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE s jsonb; algorithm text; context_bytes bytea; context_doc json; entries bigint; distinct_entries bigint;
BEGIN
 IF e='{}'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(e)<>'object' OR octet_length(e::text)>16384 OR
  e - ARRAY['account_id','selection','provider_key_id','key_identity'] <> '{}'::jsonb OR
  jsonb_typeof(e->'account_id') IS DISTINCT FROM 'string' OR e->>'account_id'<>owner::text OR
  owner='00000000-0000-0000-0000-000000000000'::uuid THEN RETURN false; END IF;
 s:=e->'selection'; algorithm:=s->>'algorithm';
 IF jsonb_typeof(s) IS DISTINCT FROM 'object' OR jsonb_typeof(s->'algorithm') IS DISTINCT FROM 'string' OR
  s - ARRAY['algorithm','key_id','bucket_key_enabled','context'] <> '{}'::jsonb THEN RETURN false; END IF;
 IF algorithm='AES256' THEN
  RETURN s='{"algorithm":"AES256"}'::jsonb AND NOT e ?| ARRAY['provider_key_id','key_identity'];
 END IF;
 IF algorithm NOT IN ('aws:kms','aws:kms:dsse') OR algorithm IS NULL OR
  jsonb_typeof(s->'key_id') IS DISTINCT FROM 'string' OR
  octet_length(s->>'key_id')>512 OR
  (s->>'key_id') !~ '^arn:gregale:kms:[a-z][a-z0-9-]{0,62}:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:key/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR
  split_part(s->>'key_id',':',5)<>owner::text OR
  split_part(s->>'key_id',':',6)='key/00000000-0000-0000-0000-000000000000' OR
  jsonb_typeof(e->'provider_key_id') IS DISTINCT FROM 'string' OR
  octet_length(e->>'provider_key_id') NOT BETWEEN 1 AND 512 OR
  (e->>'provider_key_id') ~ '[\x01-\x1f\x7f]' OR
  jsonb_typeof(e->'key_identity') IS DISTINCT FROM 'string' OR
  (e->>'key_identity') !~ '^[0-9a-f]{64}$' OR (e->>'key_identity')=repeat('0',64) OR
  (algorithm='aws:kms' AND jsonb_typeof(s->'bucket_key_enabled') IS DISTINCT FROM 'boolean') OR
  (algorithm='aws:kms:dsse' AND s ? 'bucket_key_enabled') THEN RETURN false; END IF;
 IF s ? 'context' THEN
  IF jsonb_typeof(s->'context') IS DISTINCT FROM 'string' OR octet_length(s->>'context')>10924 THEN RETURN false; END IF;
  IF s->>'context'<>'' THEN
   context_bytes:=decode(s->>'context','base64');
   IF octet_length(context_bytes)>8192 OR replace(encode(context_bytes,'base64'),E'\n','')<>s->>'context' THEN RETURN false; END IF;
   context_doc:=convert_from(context_bytes,'UTF8')::json;
   IF json_typeof(context_doc)<>'object' THEN RETURN false; END IF;
   SELECT count(*),count(DISTINCT key) INTO entries,distinct_entries FROM json_each(context_doc);
   IF entries>32 OR entries<>distinct_entries OR EXISTS(SELECT 1 FROM json_each(context_doc) WHERE key='' OR json_typeof(value)<>'string') THEN RETURN false; END IF;
  END IF;
 END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
-- +goose StatementEnd

ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS encryption_snapshot jsonb NOT NULL DEFAULT '{}' CHECK (valid_object_encryption_snapshot(encryption_snapshot,account_id));
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS encryption_dispatched boolean NOT NULL DEFAULT false;
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS encryption_verified boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_encryption_phase') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_encryption_phase CHECK (
  (encryption_snapshot='{}' OR write_phase IN ('prepared','dispatched','settled')) AND
  (NOT encryption_dispatched OR (encryption_snapshot<>'{}' AND write_phase IN ('dispatched','settled'))) AND
  (NOT encryption_verified OR (encryption_dispatched AND status='completed' AND write_phase='settled')) AND
  (encryption_snapshot='{}' OR status<>'completed' OR encryption_verified)
 );
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS encryption_snapshot jsonb NOT NULL DEFAULT '{}' CHECK (valid_object_encryption_snapshot(encryption_snapshot,account_id));
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS encryption_lease_token text NOT NULL DEFAULT '' CHECK (octet_length(encryption_lease_token)<=128);
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS encryption_verified boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_multipart_uploads'::regclass AND conname='object_multipart_encryption_phase') THEN
  ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_encryption_phase CHECK (
  (encryption_lease_token='' OR (encryption_snapshot<>'{}' AND lease_token IS NOT NULL AND encryption_lease_token=lease_token)) AND
  (NOT encryption_verified OR (encryption_snapshot<>'{}' AND state='completed' AND completion_dispatched)) AND
  (encryption_snapshot='{}' OR state<>'completed' OR encryption_verified)
 );
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_upload_encryption() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.encryption_snapshot<>'{}' AND (NEW.write_phase<>'prepared' OR NEW.status<>'pending' OR NEW.encryption_dispatched OR NEW.encryption_verified) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Encrypted write requires a prepared intent';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.encryption_snapshot IS DISTINCT FROM OLD.encryption_snapshot OR
  (OLD.encryption_snapshot<>'{}' AND (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.bytes,NEW.origin,NEW.source_key,NEW.source_etag)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.bytes,OLD.origin,OLD.source_key,OLD.source_etag)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Write encryption identity is immutable';
 END IF;
 IF OLD.encryption_snapshot<>'{}' THEN
  IF (OLD.encryption_dispatched AND NOT NEW.encryption_dispatched) OR
   (NEW.write_phase='dispatched' AND NOT NEW.encryption_dispatched) OR
   (NOT OLD.encryption_dispatched AND NEW.encryption_dispatched AND NOT (OLD.write_phase='prepared' AND NEW.write_phase='dispatched')) OR
   (NEW.encryption_verified IS DISTINCT FROM OLD.encryption_verified AND NOT (OLD.write_phase='dispatched' AND NEW.write_phase='settled' AND NEW.status='completed' AND NEW.encryption_verified)) OR
   (OLD.write_phase='settled' AND (NEW.status,NEW.etag,NEW.error_code,NEW.version_id) IS DISTINCT FROM (OLD.status,OLD.etag,OLD.error_code,OLD.version_id)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Encrypted write requires verified dispatch and settlement';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_upload_encryption_immutable ON object_upload_completions;
CREATE TRIGGER object_upload_encryption_immutable BEFORE INSERT OR UPDATE ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION protect_object_upload_encryption();

CREATE OR REPLACE FUNCTION protect_object_multipart_encryption() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.encryption_snapshot<>'{}' AND (NEW.state<>'initiating' OR NEW.lease_token IS NOT NULL OR NEW.encryption_lease_token<>'' OR NEW.encryption_verified) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Encrypted multipart requires an initiating intent';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.encryption_snapshot IS DISTINCT FROM OLD.encryption_snapshot OR
  (OLD.encryption_snapshot<>'{}' AND (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart encryption identity is immutable';
 END IF;
 IF OLD.encryption_snapshot<>'{}' THEN
  IF NEW.lease_token IS NOT NULL AND NEW.encryption_lease_token IS DISTINCT FROM NEW.lease_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Encrypted multipart requires a cipher-aware claimant';
  END IF;
  IF NEW.encryption_verified IS DISTINCT FROM OLD.encryption_verified AND NOT
   (OLD.state IN ('completing','completing_conditional') AND NEW.state='completed' AND OLD.completion_dispatched AND NEW.encryption_verified) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Encrypted multipart requires verified completion';
  END IF;
 END IF;
 IF NEW.lease_token IS NULL THEN NEW.encryption_lease_token:=''; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_encryption_immutable ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_encryption_immutable BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads
 FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_encryption();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_upload_completions WHERE encryption_snapshot<>'{}') OR
  EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE encryption_snapshot<>'{}') THEN
  RAISE EXCEPTION 'Retain encrypted write journals and completion proof before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_encryption_immutable ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_encryption();
DROP TRIGGER object_upload_encryption_immutable ON object_upload_completions;
DROP FUNCTION protect_object_upload_encryption();
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_encryption_phase,
 DROP COLUMN encryption_snapshot,DROP COLUMN encryption_lease_token,DROP COLUMN encryption_verified;
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_encryption_phase,
 DROP COLUMN encryption_snapshot,DROP COLUMN encryption_dispatched,DROP COLUMN encryption_verified;
DROP FUNCTION valid_object_encryption_snapshot(jsonb,uuid);
