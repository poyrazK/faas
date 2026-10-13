-- +goose Up
ALTER TABLE object_storage_multipart_uploads
  ADD COLUMN IF NOT EXISTS checksum_snapshot jsonb NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS checksum_verified boolean NOT NULL DEFAULT false;

-- A checksum-enabled upload cannot be completed by an older writer that knows
-- only ETags. Policy is immutable; selected parts are pinned by the completion
-- claim, and remain pinned through retries, abort cleanup and terminal replay.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_multipart_checksum_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  a text; t text; v text; p jsonb; n integer; item jsonb; i integer;
BEGIN
  IF jsonb_typeof(NEW.checksum_snapshot) <> 'object'
     OR octet_length(NEW.checksum_snapshot::text) > 2097152
     OR NEW.checksum_snapshot - ARRAY['algorithm','type','value','parts'] <> '{}' THEN
    RAISE EXCEPTION 'invalid multipart checksum snapshot' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' THEN
    IF (OLD.checksum_snapshot->>'algorithm',OLD.checksum_snapshot->>'type') IS DISTINCT FROM
       (NEW.checksum_snapshot->>'algorithm',NEW.checksum_snapshot->>'type')
       OR OLD.checksum_verified AND NOT NEW.checksum_verified
       OR OLD.state IN ('completing','completing_conditional','completed','aborting','aborted')
          AND OLD.checksum_snapshot <> NEW.checksum_snapshot THEN
      RAISE EXCEPTION 'immutable multipart checksum intent' USING ERRCODE='23514';
    END IF;
  END IF;
  IF NEW.checksum_snapshot='{}' THEN
    IF NEW.checksum_verified THEN
      RAISE EXCEPTION 'checksum verification requires an intent' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  a := NEW.checksum_snapshot->>'algorithm';
  t := NEW.checksum_snapshot->>'type';
  v := coalesce(NEW.checksum_snapshot->>'value','');
  p := coalesce(NEW.checksum_snapshot->'parts','[]');
  IF a IS NULL OR t IS NULL OR NOT (
     a IN ('CRC32','CRC32C') AND t IN ('COMPOSITE','FULL_OBJECT') OR
     a='CRC64NVME' AND t='FULL_OBJECT' OR
     a IN ('SHA1','SHA256') AND t='COMPOSITE') OR jsonb_typeof(p)<>'array' THEN
    RAISE EXCEPTION 'invalid multipart checksum policy' USING ERRCODE='23514';
  END IF;
  n := jsonb_array_length(p);
  IF NEW.state IN ('initiating','active') OR TG_OP='INSERT' THEN
    IF n<>0 OR v<>'' OR NEW.checksum_verified THEN
      RAISE EXCEPTION 'initial multipart checksum has no completion proof' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.state IN ('completing','completing_conditional','completed') THEN
    IF n<1 OR n>10000 OR n<>jsonb_array_length(NEW.completion_parts)
       OR v='' OR length(v)>128 THEN
      RAISE EXCEPTION 'multipart checksum completion requires selected parts' USING ERRCODE='23514';
    END IF;
    FOR item, i IN SELECT value, ordinality::integer FROM jsonb_array_elements(p) WITH ORDINALITY LOOP
      IF jsonb_typeof(item)<>'object' OR item - ARRAY['number','size','value']<>'{}'
         OR (item->>'number')::integer IS DISTINCT FROM i
         OR (NEW.completion_parts->(i-1)->>'part_number')::integer IS DISTINCT FROM i
         OR (item->>'size')::bigint IS NULL OR (item->>'size')::bigint NOT BETWEEN 1 AND 5368709120
         OR item->>'value' IS NULL OR length(item->>'value')>88
         OR item->>'value' !~ '^[A-Za-z0-9+/]+={0,2}$' THEN
        RAISE EXCEPTION 'invalid selected multipart checksum part' USING ERRCODE='23514';
      END IF;
    END LOOP;
  END IF;
  IF NEW.state='completed' AND NOT NEW.checksum_verified OR
     NEW.checksum_verified AND NEW.state<>'completed' THEN
    RAISE EXCEPTION 'multipart checksum result must be verified before settlement' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS object_multipart_checksum_guard ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_checksum_guard
BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads
FOR EACH ROW EXECUTE FUNCTION object_multipart_checksum_guard();

-- Checksum proof has its own narrow guard above. Preserve every original
-- receipt identity/fence rule, permitting only its new guarded proof columns.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_bound_multipart_journal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id=OLD.id) AND (
  (to_jsonb(NEW)-ARRAY['expires_at','provider_upload_id','size_bytes','part_count','part_revision','completion_parts','completion_if_match','completion_if_none_match','completion_error_code','completion_etag','completion_version_id','completion_recovery_cursor','completion_versions_observed','completion_dispatched','part_url_unsafe_until','lifecycle_scan_id','lifecycle_binding','state','lease_token','lease_until','attempt_count','retry_at','last_error_code','updated_at','encryption_lease_token','encryption_verified','protection_lease_token','protection_verified','checksum_snapshot','checksum_verified']) IS DISTINCT FROM
  (to_jsonb(OLD)-ARRAY['expires_at','provider_upload_id','size_bytes','part_count','part_revision','completion_parts','completion_if_match','completion_if_none_match','completion_error_code','completion_etag','completion_version_id','completion_recovery_cursor','completion_versions_observed','completion_dispatched','part_url_unsafe_until','lifecycle_scan_id','lifecycle_binding','state','lease_token','lease_until','attempt_count','retry_at','last_error_code','updated_at','encryption_lease_token','encryption_verified','protection_lease_token','protection_verified','checksum_snapshot','checksum_verified']) OR
  (NEW.expires_at>OLD.expires_at) OR
  (NEW.provider_upload_id IS DISTINCT FROM OLD.provider_upload_id AND NOT (OLD.state='initiating' AND OLD.provider_upload_id='' AND NEW.state='active' AND NEW.provider_upload_id<>'')) OR
  (OLD.completion_dispatched AND (NEW.size_bytes IS DISTINCT FROM OLD.size_bytes OR NEW.completion_parts IS DISTINCT FROM OLD.completion_parts OR NEW.completion_if_match IS DISTINCT FROM OLD.completion_if_match OR NEW.completion_if_none_match IS DISTINCT FROM OLD.completion_if_none_match))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_bound_journal_immutable',MESSAGE='An original provider receipt cannot be reassigned to another session intent';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE checksum_snapshot<>'{}') THEN
    RAISE EXCEPTION 'cannot remove retained multipart checksum intent' USING ERRCODE='23514';
  END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_checksum_guard ON object_storage_multipart_uploads;
DROP FUNCTION object_multipart_checksum_guard();
ALTER TABLE object_storage_multipart_uploads DROP COLUMN checksum_verified, DROP COLUMN checksum_snapshot;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_bound_multipart_journal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id=OLD.id) AND (
  (to_jsonb(NEW)-ARRAY['expires_at','provider_upload_id','size_bytes','part_count','part_revision','completion_parts','completion_if_match','completion_if_none_match','completion_error_code','completion_etag','completion_version_id','completion_recovery_cursor','completion_versions_observed','completion_dispatched','part_url_unsafe_until','lifecycle_scan_id','lifecycle_binding','state','lease_token','lease_until','attempt_count','retry_at','last_error_code','updated_at','encryption_lease_token','encryption_verified','protection_lease_token','protection_verified']) IS DISTINCT FROM
  (to_jsonb(OLD)-ARRAY['expires_at','provider_upload_id','size_bytes','part_count','part_revision','completion_parts','completion_if_match','completion_if_none_match','completion_error_code','completion_etag','completion_version_id','completion_recovery_cursor','completion_versions_observed','completion_dispatched','part_url_unsafe_until','lifecycle_scan_id','lifecycle_binding','state','lease_token','lease_until','attempt_count','retry_at','last_error_code','updated_at','encryption_lease_token','encryption_verified','protection_lease_token','protection_verified']) OR
  (NEW.expires_at>OLD.expires_at) OR
  (NEW.provider_upload_id IS DISTINCT FROM OLD.provider_upload_id AND NOT (OLD.state='initiating' AND OLD.provider_upload_id='' AND NEW.state='active' AND NEW.provider_upload_id<>'')) OR
  (OLD.completion_dispatched AND (NEW.size_bytes IS DISTINCT FROM OLD.size_bytes OR NEW.completion_parts IS DISTINCT FROM OLD.completion_parts OR NEW.completion_if_match IS DISTINCT FROM OLD.completion_if_match OR NEW.completion_if_none_match IS DISTINCT FROM OLD.completion_if_none_match))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_bound_journal_immutable',MESSAGE='An original provider receipt cannot be reassigned to another session intent';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
