-- filename: 20261005111654942_object_write_protection.sql
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION valid_object_write_protection(p jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE r jsonb; d jsonb; t timestamptz;
BEGIN
 IF p='{}' THEN RETURN true; END IF;
 IF jsonb_typeof(p)<>'object' OR octet_length(p::text)>16384 OR p-ARRAY['enabled','revision','captured_at','default_retention','requested']<>'{}' OR p->'enabled' IS DISTINCT FROM 'true'::jsonb OR
  jsonb_typeof(p->'revision') IS DISTINCT FROM 'number' AND p ? 'revision' OR coalesce((p->>'revision')::bigint,0) NOT BETWEEN 0 AND 9007199254740991 OR jsonb_typeof(p->'captured_at') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 t := (p->>'captured_at')::timestamptz;
 IF NOT isfinite(t) OR extract(year from t) NOT BETWEEN 1 AND 9999 THEN RETURN false; END IF;
 IF p ? 'default_retention' AND (NOT valid_object_lock_configuration(jsonb_build_object('enabled',true,'default_retention',p->'default_retention')) OR p->'default_retention' ? 'default_event_hold') THEN RETURN false; END IF;
 r := coalesce(p->'requested','{}'::jsonb);
 IF jsonb_typeof(r)<>'object' OR r-ARRAY['retention','legal_hold']<>'{}' THEN RETURN false; END IF;
 IF r ? 'retention' THEN
  d := r->'retention';
  IF jsonb_typeof(d)<>'object' OR d-ARRAY['mode','retain_until_date']<>'{}' OR coalesce(d->>'mode','') NOT IN ('GOVERNANCE','COMPLIANCE') OR jsonb_typeof(d->'retain_until_date') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  t := (d->>'retain_until_date')::timestamptz;
  IF NOT isfinite(t) OR extract(year from t) NOT BETWEEN 1 AND 9999 OR date_trunc('milliseconds',t)<>t THEN RETURN false; END IF;
 END IF;
 IF r ? 'legal_hold' AND (jsonb_typeof(r->'legal_hold')<>'object' OR (r->'legal_hold')-ARRAY['status']<>'{}' OR coalesce(r->'legal_hold'->>'status','') NOT IN ('ON','OFF')) THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
-- +goose StatementEnd
ALTER TABLE object_upload_completions ADD COLUMN protection_snapshot jsonb NOT NULL DEFAULT '{}' CHECK(valid_object_write_protection(protection_snapshot));
ALTER TABLE object_upload_completions ADD COLUMN protection_dispatched boolean NOT NULL DEFAULT false;
ALTER TABLE object_upload_completions ADD COLUMN protection_verified boolean NOT NULL DEFAULT false;
ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_protection_phase CHECK(
 (protection_snapshot='{}' OR write_phase IN ('prepared','dispatched','settled')) AND
 (NOT protection_dispatched OR protection_snapshot<>'{}' AND write_phase IN ('dispatched','settled')) AND
 (NOT protection_verified OR protection_dispatched AND status='completed' AND write_phase='settled' AND version_id<>'' AND version_id<>'null') AND
 (protection_snapshot='{}' OR status<>'completed' OR protection_verified));
ALTER TABLE object_storage_multipart_uploads ADD COLUMN protection_snapshot jsonb NOT NULL DEFAULT '{}' CHECK(valid_object_write_protection(protection_snapshot));
ALTER TABLE object_storage_multipart_uploads ADD COLUMN protection_lease_token text NOT NULL DEFAULT '' CHECK(octet_length(protection_lease_token)<=128);
ALTER TABLE object_storage_multipart_uploads ADD COLUMN protection_verified boolean NOT NULL DEFAULT false;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_protection_phase CHECK(
 (protection_lease_token='' OR protection_snapshot<>'{}' AND lease_token IS NOT NULL AND protection_lease_token=lease_token) AND
 (NOT protection_verified OR protection_snapshot<>'{}' AND state='completed' AND completion_dispatched AND completion_version_id<>'' AND completion_version_id<>'null') AND
 (protection_snapshot='{}' OR state<>'completed' OR protection_verified));
-- +goose StatementBegin
CREATE FUNCTION protect_object_write_protection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j object_bucket_object_lock; protected boolean; url_request jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM accounts WHERE id=NEW.account_id FOR UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT * INTO j FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id;
  protected := FOUND AND (j.enabled_required OR j.native_enabled_observed OR j.observed_snapshot->>'enabled'='true');
  IF TG_TABLE_NAME='object_upload_completions' THEN
   IF NEW.status='rejected' AND NEW.protection_snapshot='{}' THEN RETURN NEW; END IF;
   SELECT c.url_request INTO url_request FROM object_storage_s3_credentials c WHERE c.id::text=NEW.subject_id AND c.url_request IS NOT NULL;
   IF FOUND AND coalesce(url_request->'protection','{}'::jsonb) IS DISTINCT FROM coalesce(NEW.protection_snapshot->'requested','{}'::jsonb) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Signed URL protection requires its bound receipt'; END IF;
  END IF;
  IF protected OR NEW.protection_snapshot<>'{}' THEN
   IF NOT protected OR j.state<>'ready' OR NOT j.observed_known OR j.observed_snapshot->>'enabled' IS DISTINCT FROM 'true' OR
    NOT object_lock_versioning_ready(NEW.bucket_id) OR NEW.protection_snapshot='{}' OR
    coalesce((NEW.protection_snapshot->>'revision')::bigint,0)<>j.revision OR
    coalesce(NEW.protection_snapshot->'default_retention','null'::jsonb) IS DISTINCT FROM coalesce(j.observed_snapshot->'default_retention','null'::jsonb) THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='New writes require the admitted Object Lock policy';
   END IF;
   IF TG_TABLE_NAME='object_upload_completions' THEN
    IF NEW.status<>'pending' OR NEW.write_phase<>'prepared' OR NEW.protection_dispatched OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires a prepared intent'; END IF;
   ELSE
    IF NEW.state<>'initiating' OR NEW.lease_token IS NOT NULL OR NEW.protection_lease_token<>'' OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires an initiating intent'; END IF;
   END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.protection_snapshot IS DISTINCT FROM OLD.protection_snapshot OR (OLD.protection_snapshot<>'{}' AND
  (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.created_at)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Write protection snapshot and ownership are immutable';
 END IF;
 IF TG_TABLE_NAME='object_upload_completions' THEN
  IF OLD.protection_snapshot<>'{}' AND ((NEW.bytes,NEW.origin,NEW.source_key,NEW.source_etag) IS DISTINCT FROM (OLD.bytes,OLD.origin,OLD.source_key,OLD.source_etag) OR
   OLD.protection_dispatched AND NOT NEW.protection_dispatched OR NEW.write_phase='dispatched' AND NOT NEW.protection_dispatched OR
   NOT OLD.protection_dispatched AND NEW.protection_dispatched AND NOT (OLD.write_phase='prepared' AND NEW.write_phase='dispatched') OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.write_phase='dispatched' AND NEW.write_phase='settled' AND NEW.status='completed' AND NEW.protection_verified) OR
   OLD.write_phase='settled' AND (NEW.status,NEW.etag,NEW.error_code,NEW.version_id) IS DISTINCT FROM (OLD.status,OLD.etag,OLD.error_code,OLD.version_id)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires aware dispatch and verified settlement';
  END IF;
 ELSE
  IF OLD.protection_snapshot<>'{}' AND (NEW.lease_token IS NOT NULL AND NEW.protection_lease_token IS DISTINCT FROM NEW.lease_token OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.state IN ('completing','completing_conditional') AND NEW.state='completed' AND OLD.completion_dispatched AND NEW.protection_verified)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires aware claims and verified completion';
  END IF;
  IF NEW.lease_token IS NULL THEN NEW.protection_lease_token:=''; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_upload_protection_guard BEFORE INSERT OR UPDATE ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION protect_object_write_protection();
CREATE TRIGGER object_multipart_protection_guard BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_object_write_protection();
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION fence_untracked_object_lock_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NOT NULL THEN RETURN NEW; END IF;
 ELSE
  IF NEW.kind<>'proxy' OR NEW.route_receipt THEN RETURN NEW; END IF;
 END IF;
 PERFORM 1 FROM accounts a JOIN object_buckets b ON b.account_id=a.id WHERE b.id=NEW.bucket_id FOR UPDATE OF a;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND (enabled_required OR native_enabled_observed OR state<>'ready')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lock_admission_fenced',MESSAGE='Object Lock requires a tracked protection-aware write';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_lock_legacy_key_fence BEFORE INSERT ON object_storage_key_grants FOR EACH ROW EXECUTE FUNCTION fence_untracked_object_lock_write();
CREATE TRIGGER object_lock_legacy_write_fence BEFORE INSERT ON object_storage_write_admissions FOR EACH ROW EXECUTE FUNCTION fence_untracked_object_lock_write();
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE method text;
BEGIN
 IF jsonb_typeof(r)<>'object' OR octet_length(r::text)>32768 OR
  r - ARRAY['method','key','expires_in','size_bytes','content_type','cache_control','content_disposition','content_encoding','content_language','metadata','tags','encryption','multipart','protection'] <> '{}'::jsonb OR
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
 IF EXISTS(SELECT 1 FROM jsonb_each(r) WHERE key IN ('metadata','tags','encryption','protection') AND jsonb_typeof(value)<>'object') THEN RETURN false; END IF;
 IF r ? 'protection' AND (r->'protection'='{}' OR NOT valid_object_write_protection(jsonb_build_object('enabled',true,'captured_at','2026-01-01T00:00:00Z','requested',r->'protection'))) THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_upload_completions WHERE protection_snapshot<>'{}') OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE protection_snapshot<>'{}') THEN
  RAISE EXCEPTION 'Preserve write protection history until physical bucket deletion';
 END IF;
END $$;
DROP TRIGGER object_upload_protection_guard ON object_upload_completions;
DROP TRIGGER object_multipart_protection_guard ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_write_protection();
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_protection_phase, DROP COLUMN protection_snapshot, DROP COLUMN protection_dispatched, DROP COLUMN protection_verified;
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_protection_phase, DROP COLUMN protection_snapshot, DROP COLUMN protection_lease_token, DROP COLUMN protection_verified;
DROP TRIGGER object_lock_legacy_key_fence ON object_storage_key_grants;
DROP TRIGGER object_lock_legacy_write_fence ON object_storage_write_admissions;
DROP FUNCTION fence_untracked_object_lock_write();
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

DROP FUNCTION valid_object_write_protection(jsonb);
-- +goose StatementEnd
