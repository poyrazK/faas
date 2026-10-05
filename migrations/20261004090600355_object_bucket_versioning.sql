-- +goose Up
CREATE TABLE IF NOT EXISTS object_bucket_versioning (
 bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
 desired_status text NOT NULL DEFAULT '' CHECK(desired_status IN ('','Enabled','Suspended')),
 observed_status text NOT NULL DEFAULT '' CHECK(observed_status IN ('','Enabled','Suspended')),
 state text NOT NULL DEFAULT 'ready' CHECK(state IN ('ready','waiting','propagating','inventory')),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 versions_required boolean NOT NULL DEFAULT false,
 dispatched boolean NOT NULL DEFAULT false,
 propagation_until timestamptz,
 capacity_job_id uuid REFERENCES object_storage_capacity_reconciliations(id),
 lease_token text NOT NULL DEFAULT '' CHECK(octet_length(lease_token)<=128),
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT now(),
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code IN ('','untracked_writes','unsettled_writes','multipart_active','capacity_active','provider_failed','provider_mismatch','inventory_failed')),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((lease_token='')=(lease_until IS NULL)),
 CHECK(state<>'ready' OR (lease_until IS NULL AND desired_status=observed_status)),
 CHECK(state='ready' OR desired_status<>''),
 CHECK(NOT versions_required OR propagation_until IS NOT NULL),
 CHECK(NOT dispatched OR versions_required),
 CHECK(observed_status='' OR versions_required),
 CHECK(state<>'inventory' OR capacity_job_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS object_bucket_versioning_due ON object_bucket_versioning(retry_at,bucket_id) WHERE state<>'ready';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_bucket_versioning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.bucket_id<>OLD.bucket_id OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
   OR (NEW.revision<>OLD.revision AND OLD.state<>'ready')
   OR (NEW.desired_status<>OLD.desired_status AND NEW.revision<>OLD.revision+1)
   OR (NEW.revision=OLD.revision AND OLD.dispatched AND NOT NEW.dispatched) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Versioning transition identity cannot change';
  END IF;
  NEW.versions_required:=OLD.versions_required OR NEW.versions_required;
 END IF;
 IF NEW.versions_required AND NEW.state='ready' AND (TG_OP='INSERT' OR OLD.state<>'ready') AND NOT EXISTS(
  SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.id=NEW.capacity_job_id AND c.bucket_id=NEW.bucket_id
  AND c.state='completed' AND c.inventory_scope='all_versions' AND c.inventory_verified AND NEW.propagation_until<=now()
 ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Versioning cutover requires propagated configuration and verified version inventory'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_bucket_versioning_protected ON object_bucket_versioning;
CREATE TRIGGER object_bucket_versioning_protected BEFORE INSERT OR UPDATE ON object_bucket_versioning FOR EACH ROW EXECUTE FUNCTION protect_object_bucket_versioning();

CREATE OR REPLACE FUNCTION fence_object_versioning_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid; jid uuid;
BEGIN
 IF TG_TABLE_NAME='object_storage_capacity_reconciliations' THEN
  IF NEW.state<>'scanning' THEN RETURN NEW; END IF;
  bid:=NEW.bucket_id;jid:=NEW.id;
 ELSE bid:=NEW.bucket_id;
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_versioning v WHERE v.bucket_id=bid AND v.state<>'ready'
  AND (v.state<>'inventory' OR (TG_TABLE_NAME='object_storage_capacity_reconciliations' AND v.capacity_job_id IS DISTINCT FROM jid))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Configuration transition fences unrelated inventory';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_versioning_scan_fence ON object_storage_capacity_reconciliations;
CREATE TRIGGER object_versioning_scan_fence BEFORE UPDATE ON object_storage_capacity_reconciliations FOR EACH ROW EXECUTE FUNCTION fence_object_versioning_inventory();
DROP TRIGGER IF EXISTS object_versioning_rebase_fence ON object_storage_bucket_usage;
CREATE TRIGGER object_versioning_rebase_fence BEFORE UPDATE OF baseline_bytes,baseline_keys,observed_bytes,observed_keys,observed_at,inventory_scope ON object_storage_bucket_usage FOR EACH ROW EXECUTE FUNCTION fence_object_versioning_inventory();
CREATE OR REPLACE FUNCTION fence_object_capacity_write() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF NEW.state<>'deleting' OR OLD.state='deleting' THEN RETURN NEW; END IF;
  bid:=NEW.id;
 ELSE bid:=NEW.bucket_id;
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=bid AND state<>'ready') OR EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_capacity_write_fenced',MESSAGE='Object capacity reconciliation fences new writes';
 END IF;
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NULL OR (TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id) THEN
   NEW.reclaimable:=false; NEW.last_write_id:=NULL;
  ELSE
   IF NOT EXISTS (SELECT 1 FROM object_storage_write_admissions WHERE id=NEW.last_write_id AND bucket_id=bid AND key_hash=NEW.key_hash AND state='pending') THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object grant lacks matching write admission';
   END IF;
   IF TG_OP='UPDATE' THEN NEW.reclaimable:=OLD.reclaimable AND NEW.reclaimable; END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE versioned boolean;
BEGIN
 versioned:=OLD.inventory_scope='all_versions' OR (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=NEW.bucket_id AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=NEW.bucket_id AND versions_observed) OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=NEW.bucket_id AND completion_versions_observed) OR EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=NEW.bucket_id AND versions_required));
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
 IF EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=bid AND state<>'ready') OR EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
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
  IF (EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=bid AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=bid AND versions_observed) OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=bid AND completion_versions_observed) OR EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=bid AND versions_required)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Version inventory is required before new writes';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM object_bucket_versioning) THEN RAISE EXCEPTION 'Cannot discard versioning intent or observations'; END IF; END $$;
DROP TRIGGER object_versioning_scan_fence ON object_storage_capacity_reconciliations;
DROP TRIGGER object_versioning_rebase_fence ON object_storage_bucket_usage;
DROP FUNCTION fence_object_versioning_inventory();
DROP FUNCTION protect_object_bucket_versioning() CASCADE;
CREATE OR REPLACE FUNCTION fence_object_capacity_write() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF NEW.state<>'deleting' OR OLD.state='deleting' THEN RETURN NEW; END IF;
  bid:=NEW.id;
 ELSE bid:=NEW.bucket_id;
 END IF;
 IF EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_capacity_write_fenced',MESSAGE='Object capacity reconciliation fences new writes';
 END IF;
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NULL OR (TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id) THEN
   NEW.reclaimable:=false; NEW.last_write_id:=NULL;
  ELSE
   IF NOT EXISTS (SELECT 1 FROM object_storage_write_admissions WHERE id=NEW.last_write_id AND bucket_id=bid AND key_hash=NEW.key_hash AND state='pending') THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object grant lacks matching write admission';
   END IF;
   IF TG_OP='UPDATE' THEN NEW.reclaimable:=OLD.reclaimable AND NEW.reclaimable; END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;

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
DROP TABLE object_bucket_versioning;
-- +goose StatementEnd
