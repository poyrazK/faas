-- +goose Up
CREATE TABLE IF NOT EXISTS object_version_references (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 object_key text NOT NULL CHECK(octet_length(object_key) BETWEEN 1 AND 1024 AND object_key !~ '[\x01-\x1f\x7f]'),
 native_version_id text NOT NULL CHECK(octet_length(native_version_id) BETWEEN 1 AND 1024 AND native_version_id !~ '[\x01-\x1f\x7f]'),
 versions_observed boolean NOT NULL CHECK(native_version_id='null' OR versions_observed),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(bucket_id,object_key,native_version_id)
);
CREATE INDEX IF NOT EXISTS object_version_references_observed_idx ON object_version_references(bucket_id) WHERE versions_observed;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_version_reference() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM object_buckets WHERE id=OLD.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version references remain until bucket deletion';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.bucket_id IS DISTINCT FROM OLD.bucket_id OR NEW.object_key IS DISTINCT FROM OLD.object_key OR NEW.native_version_id IS DISTINCT FROM OLD.native_version_id THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version reference identity is immutable';
 END IF;
 NEW.versions_observed:=OLD.versions_observed OR NEW.versions_observed;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_version_reference_identity ON object_version_references;
CREATE TRIGGER object_version_reference_identity BEFORE UPDATE OR DELETE ON object_version_references FOR EACH ROW EXECUTE FUNCTION protect_object_version_reference();
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
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM object_version_references) THEN RAISE EXCEPTION 'Cannot discard customer version identities'; END IF; END $$;
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
DROP TABLE object_version_references;
DROP FUNCTION protect_object_version_reference();
-- +goose StatementEnd
