-- filename: 20261003214856780_object_url_capabilities.sql

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION valid_object_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
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
-- +goose StatementEnd

ALTER TABLE object_storage_s3_credentials
 ADD COLUMN url_request jsonb CHECK (url_request IS NULL OR valid_object_url_request(url_request)),
 ADD COLUMN url_api_key_id uuid CHECK (url_api_key_id IS NULL OR url_api_key_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 ADD COLUMN url_expires_at timestamptz,
 ADD COLUMN url_receipt_id uuid,
 ADD CONSTRAINT object_url_credential_shape CHECK (
  (url_request IS NULL AND url_api_key_id IS NULL AND url_expires_at IS NULL AND url_receipt_id IS NULL) OR
  (url_request IS NOT NULL AND url_expires_at IS NOT NULL AND
   url_expires_at>created_at AND url_expires_at<=created_at+interval '15 minutes' AND
   managed_app_id IS NULL AND rotation_parent_id IS NULL AND
   ((url_request->>'method'='PUT' AND permission='write' AND url_receipt_id IS NOT NULL) OR
    (url_request->>'method' IN ('GET','HEAD') AND permission='read' AND url_receipt_id IS NULL)))
 );
CREATE INDEX object_url_credentials_expiry ON object_storage_s3_credentials(bucket_id,url_expires_at) WHERE url_request IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION object_url_issuer_live(owner uuid, bucket uuid, issuer uuid, permission text, expiry timestamptz) RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT expiry>clock_timestamp() AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=bucket AND b.account_id=owner AND b.state='ready') AND
  (issuer IS NULL OR EXISTS(SELECT 1 FROM api_keys k
    WHERE k.id=issuer AND k.account_id=owner AND k.status IN ('active','grace') AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()) AND
    ('admin'=ANY(k.scopes) OR (('storage:'||permission)=ANY(k.scopes) AND EXISTS(SELECT 1 FROM object_storage_access_grants g WHERE g.api_key_id=k.id AND g.account_id=owner AND g.bucket_id=bucket AND (g.permission=permission OR g.permission='read_write'))))));
$$;

CREATE FUNCTION protect_object_url_credential() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.url_request,NEW.url_api_key_id,NEW.url_expires_at,NEW.url_receipt_id) IS DISTINCT FROM (OLD.url_request,OLD.url_api_key_id,OLD.url_expires_at,OLD.url_receipt_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL authority is immutable';
 END IF;
 IF NEW.url_request IS NOT NULL THEN
  IF TG_OP='INSERT' AND NOT object_url_issuer_live(NEW.account_id,NEW.bucket_id,NEW.url_api_key_id,NEW.permission,NEW.url_expires_at) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL requires a live owned issuer';
  END IF;
  IF TG_OP='UPDATE' AND (NEW.id,NEW.account_id,NEW.bucket_id,NEW.access_key_id,NEW.permission,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.bucket_id,OLD.access_key_id,OLD.permission,OLD.created_at) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL signing identity is immutable';
  END IF;
  IF TG_OP='UPDATE' AND OLD.status='revoked' AND NEW.status<>'revoked' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL revocation is final';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_url_credential_immutable BEFORE INSERT OR UPDATE ON object_storage_s3_credentials FOR EACH ROW EXECUTE FUNCTION protect_object_url_credential();

CREATE FUNCTION protect_object_url_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c object_storage_s3_credentials;
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT * INTO c FROM object_storage_s3_credentials WHERE id::text=OLD.subject_id AND url_request IS NOT NULL;
 ELSE
  SELECT * INTO c FROM object_storage_s3_credentials WHERE id::text=NEW.subject_id AND url_request IS NOT NULL;
 END IF;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF c.id::text<>NEW.subject_id OR c.url_receipt_id IS NULL OR c.url_receipt_id<>NEW.id OR c.account_id<>NEW.account_id OR c.bucket_id<>NEW.bucket_id OR NEW.origin<>'gateway' OR NEW.route_id IS NOT NULL OR
  NEW.object_key<>c.url_request->>'key' OR NEW.bytes<>(c.url_request->>'size_bytes')::bigint OR
  NEW.content_type<>coalesce(c.url_request->>'content_type','') OR
  coalesce(NEW.encryption_snapshot->'selection','{}')<>coalesce(c.url_request->'encryption','{}') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL requires its bound write receipt';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.write_phase<>'prepared' OR NEW.status<>'pending' THEN RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL requires a prepared write'; END IF;
  NEW.recovery_retry_at:=c.url_expires_at;
 ELSE
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.subject_id,NEW.object_key,NEW.bytes,NEW.content_type,NEW.origin,NEW.encryption_snapshot) IS DISTINCT FROM
   (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.subject_id,OLD.object_key,OLD.bytes,OLD.content_type,OLD.origin,OLD.encryption_snapshot) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL write identity is immutable';
  END IF;
  IF (OLD.write_phase='dispatched' AND NEW.write_phase NOT IN ('dispatched','settled')) OR (OLD.write_phase='prepared' AND NEW.write_phase NOT IN ('prepared','dispatched','settled')) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL write phases cannot rewind';
  END IF;
  IF NEW.write_phase='dispatched' AND OLD.write_phase<>'dispatched' AND
   (OLD.write_phase<>'prepared' OR c.status<>'active' OR NOT object_url_issuer_live(c.account_id,c.bucket_id,c.url_api_key_id,c.permission,c.url_expires_at)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL dispatch authority expired or revoked';
  END IF;
  IF OLD.write_phase='prepared' AND NEW.write_phase='prepared' AND NEW.recovery_retry_at IS DISTINCT FROM c.url_expires_at THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL preparation lasts until URL expiry';
  END IF;
  IF OLD.write_phase='settled' AND (NEW.write_phase,NEW.status,NEW.etag,NEW.error_code,NEW.version_id) IS DISTINCT FROM (OLD.write_phase,OLD.status,OLD.etag,OLD.error_code,OLD.version_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed URL terminal result is immutable';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_url_write_guard BEFORE INSERT OR UPDATE ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION protect_object_url_write();

CREATE FUNCTION object_url_receipt_committed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.url_receipt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM object_upload_completions w JOIN object_buckets b ON b.id=NEW.bucket_id
  WHERE w.id=NEW.url_receipt_id AND w.subject_id=NEW.id::text AND w.account_id=NEW.account_id AND w.bucket_id=NEW.bucket_id AND w.app_id=b.app_id AND w.origin='gateway' AND w.write_phase='prepared' AND w.status='pending') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_url_capability_fenced',MESSAGE='Signed PUT URL requires an atomic prepared receipt';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER object_url_receipt_required AFTER INSERT ON object_storage_s3_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION object_url_receipt_committed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE url_request IS NOT NULL) THEN
  RAISE EXCEPTION 'Drain signed URL capabilities and retain their write journals before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_url_receipt_required ON object_storage_s3_credentials;
DROP FUNCTION object_url_receipt_committed();
DROP TRIGGER object_url_write_guard ON object_upload_completions;
DROP FUNCTION protect_object_url_write();
DROP TRIGGER object_url_credential_immutable ON object_storage_s3_credentials;
DROP FUNCTION protect_object_url_credential();
DROP FUNCTION object_url_issuer_live(uuid,uuid,uuid,text,timestamptz);
DROP INDEX object_url_credentials_expiry;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_url_credential_shape,
 DROP COLUMN url_request,DROP COLUMN url_api_key_id,DROP COLUMN url_expires_at,DROP COLUMN url_receipt_id;
DROP FUNCTION valid_object_url_request(jsonb);
