-- filename: 20261004090600504_object_multipart_url_capabilities.sql

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE method text;
BEGIN
 IF jsonb_typeof(r)<>'object' OR octet_length(r::text)>32768 OR
  r - ARRAY['method','key','expires_in','size_bytes','content_type','cache_control','content_disposition','content_encoding','content_language','metadata','tags','encryption','multipart'] <> '{}'::jsonb OR
  jsonb_typeof(r->'method') IS DISTINCT FROM 'string' OR jsonb_typeof(r->'key') IS DISTINCT FROM 'string' OR
  octet_length(r->>'key') NOT BETWEEN 1 AND 1024 OR (r->>'key') ~ '[\x01-\x1f\x7f]' OR
  jsonb_typeof(r->'expires_in') IS DISTINCT FROM 'number' OR (r->>'expires_in')::bigint NOT BETWEEN 1 AND 900 THEN RETURN false; END IF;
 method:=r->>'method';
 IF method IN ('GET','HEAD') THEN RETURN r - ARRAY['method','key','expires_in'] = '{}'::jsonb; END IF;
 IF method<>'PUT' OR jsonb_typeof(r->'size_bytes') IS DISTINCT FROM 'number' OR (r->>'size_bytes')::bigint NOT BETWEEN 0 AND 5368709120 THEN RETURN false; END IF;
 IF r ? 'multipart' THEN
  IF jsonb_typeof(r->'multipart') IS DISTINCT FROM 'object' OR
   (r->'multipart') - ARRAY['upload_id','part_number'] <> '{}'::jsonb OR
   jsonb_typeof(r->'multipart'->'upload_id') IS DISTINCT FROM 'string' OR
   (r->'multipart'->>'upload_id')::uuid='00000000-0000-0000-0000-000000000000'::uuid OR
   jsonb_typeof(r->'multipart'->'part_number') IS DISTINCT FROM 'number' OR
   (r->'multipart'->>'part_number')::int NOT BETWEEN 1 AND 10000 OR
   (r->>'size_bytes')::bigint<1 OR r->>'content_type' IS DISTINCT FROM 'application/octet-stream' THEN RETURN false; END IF;
  RETURN r - ARRAY['method','key','expires_in','size_bytes','content_type','multipart'] = '{}'::jsonb;
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(r) WHERE key IN ('content_type','cache_control','content_disposition','content_encoding','content_language') AND (jsonb_typeof(value)<>'string' OR value::text ~ '\\r|\\n|\\u0000')) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(r) WHERE key IN ('metadata','tags','encryption') AND jsonb_typeof(value)<>'object') THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;

ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_url_credential_shape,
ADD CONSTRAINT object_url_credential_shape CHECK (
  (url_request IS NULL AND url_api_key_id IS NULL AND url_expires_at IS NULL AND url_receipt_id IS NULL) OR
  (url_request IS NOT NULL AND url_expires_at IS NOT NULL AND
   url_expires_at>created_at AND url_expires_at<=created_at+interval '15 minutes' AND
   managed_app_id IS NULL AND rotation_parent_id IS NULL AND
   ((url_request->>'method'='PUT' AND permission='write' AND ((NOT (url_request ? 'multipart') AND url_receipt_id IS NOT NULL) OR (url_request ? 'multipart' AND url_receipt_id IS NULL))) OR
    (url_request->>'method' IN ('GET','HEAD') AND permission='read' AND url_receipt_id IS NULL)))
 );

ALTER TABLE object_storage_multipart_part_grants
 DROP CONSTRAINT object_storage_multipart_part_grants_max_bytes_check,
 ADD CONSTRAINT object_storage_multipart_part_grants_max_bytes_check CHECK(max_bytes BETWEEN 0 AND 5368709120),
 ADD COLUMN url_credential_id uuid CHECK(url_credential_id IS NULL OR url_credential_id<>'00000000-0000-0000-0000-000000000000'::uuid);

CREATE FUNCTION object_url_multipart_matches(c object_storage_s3_credentials,u object_storage_multipart_uploads) RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT c.url_request ? 'multipart' AND c.permission='write' AND c.status='active' AND
 c.account_id=u.account_id AND c.bucket_id=u.bucket_id AND
 c.url_request->>'key'=u.object_key AND c.url_request->'multipart'->>'upload_id'=u.id::text AND
 u.state='active' AND u.part_count>0 AND u.provider_upload_id<>'' AND u.expires_at>clock_timestamp() AND c.url_expires_at<=u.expires_at AND
 (c.url_request->'multipart'->>'part_number')::int BETWEEN 1 AND u.part_count AND
 (c.url_request->>'size_bytes')::bigint=least(u.part_size_bytes,u.size_bytes-((c.url_request->'multipart'->>'part_number')::int-1)*u.part_size_bytes) AND
 object_url_issuer_live(c.account_id,c.bucket_id,c.url_api_key_id,c.permission,c.url_expires_at);
$$;

CREATE FUNCTION protect_object_multipart_url_credential() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads;
BEGIN
 IF NEW.url_request ? 'multipart' THEN
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=(NEW.url_request->'multipart'->>'upload_id')::uuid FOR UPDATE;
  IF NOT FOUND OR NOT object_url_multipart_matches(NEW,u) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Multipart URL requires its active owned fixed session';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_multipart_url_credential_bound BEFORE INSERT ON object_storage_s3_credentials FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_url_credential();

CREATE FUNCTION protect_object_multipart_url_part() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads; c object_storage_s3_credentials; dispatch boolean;
BEGIN
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 IF u.part_count=0 THEN
  IF NEW.max_bytes<1 OR NEW.url_credential_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Dynamic multipart parts require their capacity grant';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.max_bytes<>0 OR NEW.url_credential_id IS NULL OR NOT NEW.cleanup_tracked OR NEW.part_number>u.part_count THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Fixed multipart transfers use their existing full-object reservation';
 END IF;
 dispatch:=NEW.transfer_token IS NOT NULL;
 IF TG_OP='UPDATE' THEN
  IF (NEW.upload_id,NEW.part_number) IS DISTINCT FROM (OLD.upload_id,OLD.part_number) OR
   (NEW.transfer_token IS NOT DISTINCT FROM OLD.transfer_token AND NEW.url_credential_id IS DISTINCT FROM OLD.url_credential_id) OR
   (NEW.transfer_token IS NULL AND NEW.url_credential_id IS DISTINCT FROM OLD.url_credential_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Multipart transfer identity is immutable';
  END IF;
  dispatch:=NEW.transfer_token IS NOT NULL AND NEW.transfer_token IS DISTINCT FROM OLD.transfer_token;
  IF dispatch AND OLD.transfer_token IS NOT NULL AND OLD.unsafe_until>clock_timestamp() THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Multipart part already has an unsafe native attempt';
  END IF;
  IF NOT dispatch AND NEW.transfer_token IS NOT NULL AND NEW.unsafe_until IS DISTINCT FROM OLD.unsafe_until THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Multipart transfer deadline cannot change';
  END IF;
 END IF;
 IF dispatch THEN
  SELECT * INTO c FROM object_storage_s3_credentials WHERE id=NEW.url_credential_id;
  IF NOT FOUND OR NOT object_url_multipart_matches(c,u) OR (c.url_request->'multipart'->>'part_number')::int<>NEW.part_number OR NEW.unsafe_until<clock_timestamp()+interval '35 minutes' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Multipart part requires current fixed URL dispatch authority';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_multipart_url_part_bound BEFORE INSERT OR UPDATE ON object_storage_multipart_part_grants FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_url_part();

CREATE FUNCTION protect_object_multipart_url_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.part_count>0 AND NEW.state IN ('completing','completing_conditional','completed','aborted') AND
  EXISTS(SELECT 1 FROM object_storage_multipart_part_grants WHERE upload_id=NEW.id AND transfer_token IS NOT NULL AND unsafe_until>clock_timestamp()) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Fixed multipart transitions must drain native attempts';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_multipart_url_transition_bound BEFORE UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_url_transition();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE url_request ? 'multipart') OR
 EXISTS(SELECT 1 FROM object_storage_multipart_part_grants WHERE max_bytes=0 OR url_credential_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Drain multipart URL capabilities and tracked fixed transfers before rollback';
 END IF;
END $$;
DROP TRIGGER object_multipart_url_transition_bound ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_url_transition();
DROP TRIGGER object_multipart_url_part_bound ON object_storage_multipart_part_grants;
DROP FUNCTION protect_object_multipart_url_part();
DROP TRIGGER object_multipart_url_credential_bound ON object_storage_s3_credentials;
DROP FUNCTION protect_object_multipart_url_credential();
DROP FUNCTION object_url_multipart_matches(object_storage_s3_credentials,object_storage_multipart_uploads);
ALTER TABLE object_storage_multipart_part_grants DROP COLUMN url_credential_id,
 DROP CONSTRAINT object_storage_multipart_part_grants_max_bytes_check,
 ADD CONSTRAINT object_storage_multipart_part_grants_max_bytes_check CHECK(max_bytes>0 AND max_bytes<=5368709120);

CREATE OR REPLACE FUNCTION valid_object_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE method text;
BEGIN
 IF jsonb_typeof(r)<>'object' OR octet_length(r::text)>32768 OR
  r - ARRAY['method','key','expires_in','size_bytes','content_type','cache_control','content_disposition','content_encoding','content_language','metadata','tags','encryption'] <> '{}'::jsonb OR
  jsonb_typeof(r->'method') IS DISTINCT FROM 'string' OR jsonb_typeof(r->'key') IS DISTINCT FROM 'string' OR
  octet_length(r->>'key') NOT BETWEEN 1 AND 1024 OR (r->>'key') ~ '[\x01-\x1f\x7f]' OR
  jsonb_typeof(r->'expires_in') IS DISTINCT FROM 'number' OR (r->>'expires_in')::bigint NOT BETWEEN 1 AND 900 THEN RETURN false; END IF;
 method:=r->>'method';
 IF method IN ('GET','HEAD') THEN RETURN r - ARRAY['method','key','expires_in'] = '{}'::jsonb; END IF;
 IF method<>'PUT' OR jsonb_typeof(r->'size_bytes') IS DISTINCT FROM 'number' OR (r->>'size_bytes')::bigint NOT BETWEEN 0 AND 5368709120 THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(r) WHERE key IN ('content_type','cache_control','content_disposition','content_encoding','content_language') AND (jsonb_typeof(value)<>'string' OR value::text ~ '\\r|\\n|\\u0000')) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(r) WHERE key IN ('metadata','tags','encryption') AND jsonb_typeof(value)<>'object') THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;

ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_url_credential_shape,
ADD CONSTRAINT object_url_credential_shape CHECK (
  (url_request IS NULL AND url_api_key_id IS NULL AND url_expires_at IS NULL AND url_receipt_id IS NULL) OR
  (url_request IS NOT NULL AND url_expires_at IS NOT NULL AND
   url_expires_at>created_at AND url_expires_at<=created_at+interval '15 minutes' AND
   managed_app_id IS NULL AND rotation_parent_id IS NULL AND
   ((url_request->>'method'='PUT' AND permission='write' AND url_receipt_id IS NOT NULL) OR
    (url_request->>'method' IN ('GET','HEAD') AND permission='read' AND url_receipt_id IS NULL)))
 );
-- +goose StatementEnd
