-- +goose Up
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS completion_etag text NOT NULL DEFAULT '' CHECK (octet_length(completion_etag)<=256 AND completion_etag !~ '[\x01-\x1f\x7f]' AND (completion_etag='' OR btrim(completion_etag)<>''));
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS completion_version_id text NOT NULL DEFAULT '' CHECK (completion_version_id='' OR completion_version_id='null' OR completion_version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS completion_recovery_cursor text NOT NULL DEFAULT '' CHECK (octet_length(completion_recovery_cursor)<=8192 AND completion_recovery_cursor ~ '^[A-Za-z0-9_-]*$');
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS completion_versions_observed boolean NOT NULL DEFAULT false;
-- Existing in-flight completions may have reached the provider before rollout.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='object_storage_multipart_uploads'::regclass AND attname='completion_dispatched' AND NOT attisdropped) THEN
  ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS completion_dispatched boolean NOT NULL DEFAULT false;
  UPDATE object_storage_multipart_uploads SET completion_dispatched=true WHERE state IN ('completing','completing_conditional');
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_multipart_uploads'::regclass AND conname='object_multipart_result_shape') THEN
  ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_result_shape CHECK (
  (state='completed' OR (completion_etag='' AND completion_version_id=''))
  AND (completion_version_id='' OR completion_etag<>'')
  AND (state<>'completed' OR completion_recovery_cursor='')
  AND (state<>'completed' OR NOT completion_dispatched OR completion_etag<>'')
 );
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_multipart_result() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.completion_etag<>'' OR NEW.completion_version_id<>'' OR NEW.completion_recovery_cursor<>'' OR NEW.completion_dispatched OR NEW.completion_versions_observed THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart result requires a dispatched completion';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.completion_dispatched AND NOT NEW.completion_dispatched THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart dispatch cannot be forgotten';
 END IF;
 IF NOT OLD.completion_dispatched AND NEW.completion_dispatched
  AND NOT (OLD.state IN ('completing','completing_conditional') AND NEW.state=OLD.state) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart dispatch requires a completing intent';
 END IF;
 IF (NEW.completion_etag IS DISTINCT FROM OLD.completion_etag OR NEW.completion_version_id IS DISTINCT FROM OLD.completion_version_id)
  AND NOT (OLD.state IN ('completing','completing_conditional') AND NEW.state='completed' AND OLD.completion_dispatched) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart result is immutable';
 END IF;
 IF NEW.completion_version_id<>'' AND NOT EXISTS(SELECT 1 FROM object_version_references v WHERE v.bucket_id=NEW.bucket_id AND v.object_key=NEW.object_key
  AND ((NEW.completion_version_id='null' AND v.native_version_id='null') OR (v.id::text=NEW.completion_version_id AND v.native_version_id<>'null'))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart version must belong to its bucket and key';
 END IF;
 NEW.completion_versions_observed:=OLD.completion_versions_observed OR NEW.completion_versions_observed;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_result_immutable ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_result_immutable BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_result();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE versioned boolean;
BEGIN
 versioned:=OLD.inventory_scope='all_versions' OR (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=NEW.bucket_id AND versions_observed) OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=NEW.bucket_id AND completion_versions_observed));
 IF OLD.inventory_scope='all_versions' AND NEW.inventory_scope<>'all_versions' THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version accounting cannot revert to current-object inventory';
 END IF;
 IF versioned AND EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning')
  AND NOT EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning' AND inventory_scope='all_versions' AND inventory_verified AND lease_until>now()) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_version_reclamation_fenced',MESSAGE='Retained versions require verified version inventory';
 END IF;
 IF versioned AND (NEW.inventory_scope IS DISTINCT FROM OLD.inventory_scope
  OR NEW.baseline_bytes IS DISTINCT FROM OLD.baseline_bytes OR NEW.baseline_keys IS DISTINCT FROM OLD.baseline_keys
  OR NEW.observed_bytes IS DISTINCT FROM OLD.observed_bytes OR NEW.observed_keys IS DISTINCT FROM OLD.observed_keys
  OR NEW.observed_at IS DISTINCT FROM OLD.observed_at)
  AND NOT EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning' AND inventory_scope='all_versions' AND inventory_verified AND lease_until>now()) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require verified version observations';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION fence_object_native_version_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mode text; bid uuid;
BEGIN
 bid:=NEW.bucket_id;
 IF EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Capacity inventory fences new writes';
 END IF;
 SELECT inventory_scope INTO mode FROM object_storage_bucket_usage WHERE bucket_id=bid;
 IF mode='all_versions' THEN
  IF TG_TABLE_NAME='object_storage_key_grants' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require per-attempt admission';
  ELSIF NOT NEW.native_version THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require per-attempt admission';
  END IF;
 ELSE
  IF TG_TABLE_NAME='object_storage_write_admissions' THEN
   IF NEW.native_version THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before version admission'; END IF;
  END IF;
  IF (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=bid AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=bid AND versions_observed) OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=bid AND completion_versions_observed)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before new writes';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE completion_dispatched OR completion_versions_observed OR completion_recovery_cursor<>'' OR completion_etag<>'' OR completion_version_id<>'') THEN
  RAISE EXCEPTION 'Cannot discard multipart completion identities or recovery state';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE versioned boolean;
BEGIN
 versioned:=OLD.inventory_scope='all_versions' OR (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=NEW.bucket_id AND versions_observed));
 IF OLD.inventory_scope='all_versions' AND NEW.inventory_scope<>'all_versions' THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version accounting cannot revert to current-object inventory';
 END IF;
 IF versioned AND EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning')
  AND NOT EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning' AND inventory_scope='all_versions' AND inventory_verified AND lease_until>now()) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_version_reclamation_fenced',MESSAGE='Retained versions require verified version inventory';
 END IF;
 IF versioned AND (NEW.inventory_scope IS DISTINCT FROM OLD.inventory_scope
  OR NEW.baseline_bytes IS DISTINCT FROM OLD.baseline_bytes OR NEW.baseline_keys IS DISTINCT FROM OLD.baseline_keys
  OR NEW.observed_bytes IS DISTINCT FROM OLD.observed_bytes OR NEW.observed_keys IS DISTINCT FROM OLD.observed_keys
  OR NEW.observed_at IS DISTINCT FROM OLD.observed_at)
  AND NOT EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning' AND inventory_scope='all_versions' AND inventory_verified AND lease_until>now()) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require verified version observations';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION fence_object_native_version_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mode text; bid uuid;
BEGIN
 bid:=NEW.bucket_id;
 IF EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Capacity inventory fences new writes';
 END IF;
 SELECT inventory_scope INTO mode FROM object_storage_bucket_usage WHERE bucket_id=bid;
 IF mode='all_versions' THEN
  IF TG_TABLE_NAME='object_storage_key_grants' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require per-attempt admission';
  ELSIF NOT NEW.native_version THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Retained versions require per-attempt admission';
  END IF;
 ELSE
  IF TG_TABLE_NAME='object_storage_write_admissions' THEN
   IF NEW.native_version THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before version admission'; END IF;
  END IF;
  IF (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=bid AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=bid AND versions_observed)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before new writes';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER object_multipart_result_immutable ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_result();
-- +goose StatementEnd
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_result_shape,
 DROP COLUMN completion_etag, DROP COLUMN completion_version_id, DROP COLUMN completion_recovery_cursor,
 DROP COLUMN completion_versions_observed, DROP COLUMN completion_dispatched;
