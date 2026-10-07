-- +goose Up
-- ADR-590: bind only newly reserved sessions; never adopt legacy receipts.
ALTER TABLE object_bucket_mutations ADD COLUMN IF NOT EXISTS multipart_upload_id uuid UNIQUE REFERENCES object_storage_multipart_uploads(id) ON DELETE RESTRICT;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='object_bucket_mutations'::regclass AND conname='object_mutation_single_owner') THEN
  ALTER TABLE object_bucket_mutations ADD CONSTRAINT object_mutation_single_owner CHECK(upload_id IS NULL OR multipart_upload_id IS NULL);
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_bound_multipart_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF OLD.multipart_upload_id IS NOT NULL OR NEW.multipart_upload_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_immutable',MESSAGE='Original multipart receipts are immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.multipart_upload_id IS NOT NULL AND EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE id=OLD.multipart_upload_id AND state NOT IN ('completed','aborted')) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_unsettled',MESSAGE='Settle the original session before removing its provider receipt';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.multipart_upload_id IS NOT NULL THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_isolation',MESSAGE='Multipart receipt binding requires READ COMMITTED';
  END IF;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
  PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
  PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_capture_fenced',MESSAGE='Capture fences new receipt binding';
  END IF;
  IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NEW.kind<>'request' OR NOT EXISTS(SELECT 1 FROM object_buckets WHERE id=u.bucket_id AND account_id=u.account_id AND app_id=u.app_id AND backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint AND physical_name=NEW.physical_name AND state='ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_original',MESSAGE='Bind only the original reserved session placement';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_multipart_mutation_guard ON object_bucket_mutations;
CREATE TRIGGER object_multipart_mutation_guard BEFORE INSERT OR UPDATE OR DELETE ON object_bucket_mutations FOR EACH ROW EXECUTE FUNCTION guard_bound_multipart_mutation();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compose_object_multipart_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.state='initiating' AND NEW.provider_upload_id='' THEN
  INSERT INTO object_bucket_mutations(id,bucket_id,kind,backend_id,backend_fingerprint,physical_name,multipart_upload_id)
  SELECT NEW.id,id,'request',backend_id,backend_fingerprint,physical_name,NEW.id FROM object_buckets WHERE id=NEW.bucket_id;
 ELSIF TG_OP='UPDATE' AND NEW.state IN ('completed','aborted') THEN
  DELETE FROM object_bucket_mutations WHERE multipart_upload_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_multipart_mutation_composition ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_mutation_composition AFTER INSERT OR UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION compose_object_multipart_mutation();
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
DROP TRIGGER IF EXISTS object_multipart_bound_journal ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_bound_journal BEFORE UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_bound_multipart_journal();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id IS NOT NULL) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_down_busy',MESSAGE='Settle bound sessions and release capture holds before removing receipt ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_bound_journal ON object_storage_multipart_uploads;
DROP FUNCTION protect_bound_multipart_journal();
DROP TRIGGER object_multipart_mutation_composition ON object_storage_multipart_uploads;
DROP FUNCTION compose_object_multipart_mutation();
DROP TRIGGER object_multipart_mutation_guard ON object_bucket_mutations;
DROP FUNCTION guard_bound_multipart_mutation();
ALTER TABLE object_bucket_mutations DROP CONSTRAINT object_mutation_single_owner, DROP COLUMN multipart_upload_id;
