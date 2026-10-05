-- +goose Up
ALTER TABLE object_storage_bucket_usage ADD COLUMN IF NOT EXISTS inventory_scope text NOT NULL DEFAULT 'current' CHECK(inventory_scope IN ('current','all_versions'));
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS inventory_scope text NOT NULL DEFAULT 'current' CHECK(inventory_scope IN ('current','all_versions'));
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS inventory_cursor text NOT NULL DEFAULT '' CHECK(octet_length(inventory_cursor)<=8192);
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS inventory_verified boolean NOT NULL DEFAULT false;
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS scanned_pages bigint NOT NULL DEFAULT 0 CHECK(scanned_pages BETWEEN 0 AND 1000);
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS scanned_bytes bigint NOT NULL DEFAULT 0 CHECK(scanned_bytes BETWEEN 0 AND 1152921504606846976);
ALTER TABLE object_storage_capacity_reconciliations ADD COLUMN IF NOT EXISTS scanned_versions bigint NOT NULL DEFAULT 0 CHECK(scanned_versions BETWEEN 0 AND 1000000);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_capacity_reconciliations'::regclass AND conname='object_storage_capacity_reconciliations_check3') THEN
  ALTER TABLE object_storage_capacity_reconciliations ADD CONSTRAINT object_storage_capacity_reconciliations_check3 CHECK(NOT inventory_verified OR inventory_scope='all_versions');
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_storage_write_admissions ADD COLUMN IF NOT EXISTS native_version boolean NOT NULL DEFAULT false;
ALTER TABLE object_storage_write_admissions ADD COLUMN IF NOT EXISTS native_bytes bigint NOT NULL DEFAULT 0 CHECK(native_bytes>=0);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_write_admissions'::regclass AND conname='object_storage_write_admissions_check2') THEN
  ALTER TABLE object_storage_write_admissions ADD CONSTRAINT object_storage_write_admissions_check2 CHECK(native_version OR native_bytes=0);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS object_storage_version_inventory_entries (
 job_id uuid NOT NULL REFERENCES object_storage_capacity_reconciliations(id) ON DELETE CASCADE,
 identity_hash text NOT NULL CHECK(identity_hash ~ '^[a-f0-9]{64}$'),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 0 AND 5497558138880),
 PRIMARY KEY(job_id,identity_hash)
);
CREATE TABLE IF NOT EXISTS object_storage_version_inventory_cursors (
 job_id uuid NOT NULL REFERENCES object_storage_capacity_reconciliations(id) ON DELETE CASCADE,
 cursor_hash text NOT NULL CHECK(cursor_hash ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(job_id,cursor_hash)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE versioned boolean;
BEGIN
 versioned:=OLD.inventory_scope='all_versions' OR EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed);
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
  IF EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=bid AND recovery_versions_observed) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before new writes';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_version_reclamation_fence ON object_storage_bucket_usage;
DROP TRIGGER IF EXISTS object_version_reclamation_fence ON object_storage_bucket_usage;
CREATE TRIGGER object_version_reclamation_fence
 BEFORE UPDATE OF baseline_bytes,baseline_keys,granted_bytes,granted_keys,observed_bytes,observed_keys,observed_at,inventory_scope ON object_storage_bucket_usage
 FOR EACH ROW EXECUTE FUNCTION fence_object_version_reclamation();
DROP TRIGGER IF EXISTS object_native_version_write_fence ON object_storage_write_admissions;
CREATE TRIGGER object_native_version_write_fence BEFORE INSERT ON object_storage_write_admissions
 FOR EACH ROW EXECUTE FUNCTION fence_object_native_version_admission();
DROP TRIGGER IF EXISTS object_native_version_key_grant_fence ON object_storage_key_grants;
CREATE TRIGGER object_native_version_key_grant_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants
 FOR EACH ROW EXECUTE FUNCTION fence_object_native_version_admission();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_bucket_usage WHERE inventory_scope='all_versions')
  OR EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE inventory_scope='all_versions' AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION 'Cannot roll back active native version accounting';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_native_version_write_fence ON object_storage_write_admissions;
DROP TRIGGER object_native_version_key_grant_fence ON object_storage_key_grants;
DROP FUNCTION fence_object_native_version_admission();
DROP TABLE object_storage_version_inventory_entries;
DROP TABLE object_storage_version_inventory_cursors;
ALTER TABLE object_storage_write_admissions DROP COLUMN native_bytes,DROP COLUMN native_version;
ALTER TABLE object_storage_capacity_reconciliations DROP COLUMN inventory_scope,DROP COLUMN inventory_cursor,DROP COLUMN inventory_verified,DROP COLUMN scanned_pages,DROP COLUMN scanned_bytes,DROP COLUMN scanned_versions;
DROP TRIGGER object_version_reclamation_fence ON object_storage_bucket_usage;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed)
  AND EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=NEW.bucket_id AND state='scanning') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_version_reclamation_fenced',MESSAGE='Retained versions require version-aware accounting';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE object_storage_bucket_usage DROP COLUMN inventory_scope;
CREATE TRIGGER object_version_reclamation_fence BEFORE UPDATE OF baseline_bytes,baseline_keys,granted_bytes,granted_keys ON object_storage_bucket_usage
 FOR EACH ROW EXECUTE FUNCTION fence_object_version_reclamation();
