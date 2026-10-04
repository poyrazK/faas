-- +goose Up
CREATE TABLE IF NOT EXISTS object_deletions (
 id uuid PRIMARY KEY,
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 object_key text NOT NULL CHECK(octet_length(object_key) BETWEEN 1 AND 1024),
 selector text NOT NULL DEFAULT '' CHECK(selector IN ('','null')),
 state text NOT NULL CHECK(state IN ('prepared','dispatched','completed','failed')),
 provider_status text NOT NULL DEFAULT '' CHECK(provider_status IN ('','Enabled','Suspended')),
 baseline jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(baseline)='array' AND jsonb_array_length(baseline)<=4096 AND octet_length(baseline::text)<=278530),
 provider_version_id text NOT NULL DEFAULT '' CHECK(octet_length(provider_version_id)<=1024),
 version_id text NOT NULL DEFAULT '' CHECK(version_id='' OR version_id='null' OR version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 delete_marker boolean NOT NULL DEFAULT false,
 reserved_bytes bigint NOT NULL DEFAULT 0 CHECK(reserved_bytes>=0 AND reserved_bytes<=1024),
 lease_token text NOT NULL DEFAULT '' CHECK(octet_length(lease_token)<=128),
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT now(),
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code IN ('','provider_uncertain','configuration','preparation_failed','preparation_expired','provider_rejected')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((lease_token='')=(lease_until IS NULL)),
 CHECK(state IN ('prepared','dispatched') OR lease_token=''),
 CHECK(state<>'failed' OR last_error_code IN ('preparation_failed','preparation_expired','provider_rejected')),
 CHECK(state<>'completed' OR last_error_code=''),
 CHECK(state<>'completed' OR selector<>'null' OR version_id='null'),
 CHECK(state<>'completed' OR provider_status<>'Enabled' OR selector<>'' OR (delete_marker AND version_id NOT IN ('','null') AND provider_version_id NOT IN ('','null'))),
 CHECK(reserved_bytes=0 OR (selector='' AND provider_status<>'')),
 CHECK(provider_status='Enabled' OR baseline='[]')
);
CREATE UNIQUE INDEX IF NOT EXISTS object_deletions_active_bucket ON object_deletions(bucket_id) WHERE state IN ('prepared','dispatched');
CREATE INDEX IF NOT EXISTS object_deletions_due ON object_deletions(retry_at,id) WHERE state IN ('prepared','dispatched');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key OR NEW.selector<>OLD.selector
  OR NEW.created_at<>OLD.created_at OR NEW.provider_status<>OLD.provider_status OR NEW.reserved_bytes<>OLD.reserved_bytes
  OR (OLD.state<>'prepared' AND NEW.baseline<>OLD.baseline)
  OR (OLD.state='dispatched' AND NEW.state NOT IN ('dispatched','completed') AND NOT (NEW.state='failed' AND NEW.last_error_code='provider_rejected'))
  OR OLD.state IN ('completed','failed') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Deletion identity and dispatched attempt are immutable';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_deletion_protected ON object_deletions;
CREATE TRIGGER object_deletion_protected BEFORE UPDATE ON object_deletions FOR EACH ROW EXECUTE FUNCTION protect_object_deletion();
CREATE OR REPLACE FUNCTION fence_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF TG_OP='DELETE' THEN bid:=OLD.id;
  ELSE IF NEW.state=OLD.state OR NEW.state NOT IN ('deleting','deleted') THEN RETURN NEW; END IF; bid:=NEW.id; END IF;
 ELSE
  bid:=NEW.bucket_id;
  IF TG_TABLE_NAME='object_bucket_versioning' THEN IF NEW.state='ready' THEN RETURN NEW; END IF; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_deletions d WHERE d.bucket_id=bid AND d.state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_fenced',MESSAGE='Unsettled deletion fences mutation and inventory';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_deletion_write_fence ON object_storage_write_admissions;
CREATE TRIGGER object_deletion_write_fence BEFORE INSERT ON object_storage_write_admissions FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_grant_fence ON object_storage_key_grants;
CREATE TRIGGER object_deletion_grant_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_multipart_fence ON object_storage_multipart_uploads;
CREATE TRIGGER object_deletion_multipart_fence BEFORE INSERT ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_bucket_fence ON object_buckets;
CREATE TRIGGER object_deletion_bucket_fence BEFORE UPDATE OR DELETE ON object_buckets FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_configuration_fence ON object_bucket_versioning;
CREATE TRIGGER object_deletion_configuration_fence BEFORE INSERT OR UPDATE ON object_bucket_versioning FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_capacity_fence ON object_storage_capacity_reconciliations;
CREATE TRIGGER object_deletion_capacity_fence BEFORE INSERT OR UPDATE ON object_storage_capacity_reconciliations FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
DROP TRIGGER IF EXISTS object_deletion_inventory_fence ON object_storage_bucket_usage;
CREATE TRIGGER object_deletion_inventory_fence BEFORE UPDATE OF baseline_bytes,baseline_keys,observed_bytes,observed_keys,observed_at,inventory_scope ON object_storage_bucket_usage FOR EACH ROW EXECUTE FUNCTION fence_object_deletion();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM object_deletions) THEN RAISE EXCEPTION 'Cannot discard deletion receipts or uncertain attempts'; END IF; END $$;
DROP TRIGGER object_deletion_write_fence ON object_storage_write_admissions;
DROP TRIGGER object_deletion_grant_fence ON object_storage_key_grants;
DROP TRIGGER object_deletion_multipart_fence ON object_storage_multipart_uploads;
DROP TRIGGER object_deletion_bucket_fence ON object_buckets;
DROP TRIGGER object_deletion_configuration_fence ON object_bucket_versioning;
DROP TRIGGER object_deletion_capacity_fence ON object_storage_capacity_reconciliations;
DROP TRIGGER object_deletion_inventory_fence ON object_storage_bucket_usage;
DROP FUNCTION fence_object_deletion();
DROP FUNCTION protect_object_deletion() CASCADE;
DROP TABLE object_deletions;
-- +goose StatementEnd
