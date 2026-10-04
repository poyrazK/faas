-- +goose Up
ALTER TABLE object_storage_capacity_reconciliations
 DROP CONSTRAINT object_storage_capacity_reconciliations_last_error_code_check,
 ADD CONSTRAINT object_storage_capacity_reconciliations_last_error_code_check
 CHECK (last_error_code IN ('','untracked_writes','unsettled_writes','multipart_active','deadline','inventory_failed','version_accounting_required'));

-- +goose StatementBegin
CREATE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed)
  AND EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_version_reclamation_fenced',MESSAGE='Retained versions require version-aware accounting';
 END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION retain_object_version_history_latch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.recovery_versions_observed THEN NEW.recovery_versions_observed:=true; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER object_version_reclamation_fence
 BEFORE UPDATE OF baseline_bytes,baseline_keys,granted_bytes,granted_keys ON object_storage_bucket_usage
 FOR EACH ROW EXECUTE FUNCTION fence_object_version_reclamation();
CREATE TRIGGER object_version_history_latch
 BEFORE UPDATE OF recovery_versions_observed ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION retain_object_version_history_latch();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_upload_completions WHERE recovery_versions_observed) THEN
  RAISE EXCEPTION 'Cannot remove accounting fence for retained versions';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_version_history_latch ON object_upload_completions;
DROP TRIGGER object_version_reclamation_fence ON object_storage_bucket_usage;
DROP FUNCTION retain_object_version_history_latch();
DROP FUNCTION fence_object_version_reclamation();
ALTER TABLE object_storage_capacity_reconciliations
 DROP CONSTRAINT object_storage_capacity_reconciliations_last_error_code_check,
 ADD CONSTRAINT object_storage_capacity_reconciliations_last_error_code_check
 CHECK (last_error_code IN ('','untracked_writes','unsettled_writes','multipart_active','deadline','inventory_failed'));
