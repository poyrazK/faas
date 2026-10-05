-- filename: 20261004090600544_object_bucket_object_lock.sql
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_lock_period(p jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; n numeric;
BEGIN
 IF jsonb_typeof(p) IS DISTINCT FROM 'object' OR (p ? 'days')=(p ? 'years') THEN RETURN false; END IF;
 FOR k IN SELECT jsonb_object_keys(p) LOOP IF k NOT IN ('days','years') THEN RETURN false; END IF; END LOOP;
 k:=CASE WHEN p ? 'days' THEN 'days' ELSE 'years' END;
 IF jsonb_typeof(p->k) IS DISTINCT FROM 'number' OR (p->>k)!~'^[0-9]+$' THEN RETURN false; END IF;
 n:=(p->>k)::numeric;
 RETURN n>0 AND n<=CASE WHEN k='days' THEN 36500 ELSE 100 END;
END $$;

CREATE OR REPLACE FUNCTION valid_object_lock_configuration(c jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; r jsonb; p jsonb;
BEGIN
 IF jsonb_typeof(c) IS DISTINCT FROM 'object' OR octet_length(c::text)>16384 OR
  jsonb_typeof(c->'enabled') IS DISTINCT FROM 'boolean' THEN RETURN false; END IF;
 FOR k IN SELECT jsonb_object_keys(c) LOOP IF k NOT IN ('enabled','default_retention') THEN RETURN false; END IF; END LOOP;
 IF NOT (c ? 'default_retention') THEN RETURN true; END IF;
 r:=c->'default_retention';
 IF c->>'enabled'<>'true' OR jsonb_typeof(r) IS DISTINCT FROM 'object' OR
  jsonb_typeof(r->'mode') IS DISTINCT FROM 'string' OR r->>'mode' NOT IN ('GOVERNANCE','COMPLIANCE') THEN RETURN false; END IF;
 FOR k IN SELECT jsonb_object_keys(r) LOOP IF k NOT IN ('mode','days','years','default_event_hold') THEN RETURN false; END IF; END LOOP;
 p:=r-'mode'-'default_event_hold';
 IF p<>'{}' AND NOT valid_object_lock_period(p) THEN RETURN false; END IF;
 IF r ? 'default_event_hold' AND NOT valid_object_lock_period(r->'default_event_hold') THEN RETURN false; END IF;
 RETURN p<>'{}' OR r ? 'default_event_hold';
END $$;

CREATE TABLE IF NOT EXISTS object_bucket_object_lock (
 bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
 account_id uuid NOT NULL, app_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('ready','waiting','applying')),
 revision bigint NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 enabled_required boolean NOT NULL DEFAULT false, native_enabled_observed boolean NOT NULL DEFAULT false,
 observed_known boolean NOT NULL DEFAULT false,
 observed_snapshot jsonb NOT NULL DEFAULT 'null', desired_snapshot jsonb NOT NULL DEFAULT 'null',
 lease_token text NOT NULL DEFAULT '' CHECK(octet_length(lease_token)<=128), lease_until timestamptz,
 retry_at timestamptz NOT NULL, dispatched boolean NOT NULL DEFAULT false,
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code IN ('','versioning_pending','untracked_writes','unsettled_writes','multipart_active','capacity_active','provider_failed','provider_mismatch','provider_unsupported')),
 updated_at timestamptz NOT NULL,
 CHECK(observed_known=(observed_snapshot<>'null')),
 CHECK(observed_snapshot='null' OR valid_object_lock_configuration(observed_snapshot)),
 CHECK(desired_snapshot='null' OR (valid_object_lock_configuration(desired_snapshot) AND desired_snapshot->>'enabled'='true' AND enabled_required)),
 CHECK((revision=0)=(desired_snapshot='null')),
 CHECK(NOT native_enabled_observed OR enabled_required),
 CHECK(observed_snapshot->>'enabled' IS DISTINCT FROM 'true' OR (native_enabled_observed AND enabled_required)),
 CHECK((state='applying' AND lease_token<>'' AND lease_until IS NOT NULL) OR (state<>'applying' AND lease_token='' AND lease_until IS NULL)),
 CHECK(state<>'ready' OR observed_known),
 CHECK(state<>'ready' OR (NOT enabled_required OR (observed_known AND observed_snapshot->>'enabled'='true'))),
 CHECK(state<>'ready' OR desired_snapshot='null' OR desired_snapshot=observed_snapshot),
 CHECK(NOT dispatched OR desired_snapshot<>'null')
);
CREATE INDEX IF NOT EXISTS object_bucket_object_lock_due ON object_bucket_object_lock(retry_at,bucket_id) WHERE state<>'ready';

CREATE OR REPLACE FUNCTION object_lock_versioning_ready(bid uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM object_bucket_versioning v JOIN object_storage_capacity_reconciliations c ON c.id=v.capacity_job_id
 WHERE v.bucket_id=bid AND v.state='ready' AND v.desired_status='Enabled' AND v.observed_status='Enabled'
 AND v.versions_required AND v.propagation_until<=now() AND c.bucket_id=bid AND c.state='completed'
 AND c.inventory_scope='all_versions' AND c.inventory_verified AND c.created_at>=v.propagation_until)
$$;

CREATE OR REPLACE FUNCTION object_lock_drained(bid uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=bid AND state IN ('prepared','dispatched'))
 AND NOT EXISTS(SELECT 1 FROM object_storage_write_admissions w LEFT JOIN object_storage_multipart_uploads m ON m.id=w.multipart_upload_id
  WHERE w.bucket_id=bid AND ((w.kind='proxy' AND w.state='pending') OR (w.kind='multipart' AND m.state NOT IN ('completed','aborted'))))
 AND NOT EXISTS(SELECT 1 FROM object_storage_key_grants WHERE bucket_id=bid AND NOT reclaimable)
 AND NOT EXISTS(SELECT 1 FROM object_storage_multipart_uploads m WHERE m.bucket_id=bid AND
  (m.state NOT IN ('completed','aborted') OR (m.state<>'completed' AND EXISTS(SELECT 1 FROM object_storage_multipart_part_grants p WHERE p.upload_id=m.id))))
 AND NOT EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=bid AND state IN ('waiting','scanning'))
$$;

CREATE OR REPLACE FUNCTION protect_object_bucket_object_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b object_buckets;
BEGIN
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve permanent Object Lock history until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR b.state<>'ready' OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock configuration requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision>1 OR NEW.state='applying' OR NEW.dispatched THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires an initial observation or durable request';
  END IF;
 ELSE
  IF (NEW.bucket_id,NEW.account_id,NEW.app_id) IS DISTINCT FROM (OLD.bucket_id,OLD.account_id,OLD.app_id)
   OR (OLD.enabled_required AND NOT NEW.enabled_required) OR (OLD.native_enabled_observed AND NOT NEW.native_enabled_observed)
   OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
   OR (NEW.revision=OLD.revision AND (NEW.desired_snapshot IS DISTINCT FROM OLD.desired_snapshot OR (OLD.dispatched AND NOT NEW.dispatched)))
   OR (NEW.revision<>OLD.revision AND (NEW.state<>'waiting' OR NEW.dispatched OR
    (OLD.state<>'ready' AND NOT (OLD.revision=0 AND OLD.desired_snapshot='null' AND (OLD.lease_until IS NULL OR OLD.lease_until<=clock_timestamp())))))
   OR (NEW.state='applying' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token AND
    ((OLD.lease_until IS NOT NULL AND OLD.lease_until>clock_timestamp()) OR OLD.retry_at>clock_timestamp()))
   OR (OLD.state<>'ready' AND NEW.state='ready' AND NOT
    (OLD.state='applying' AND OLD.lease_until>clock_timestamp() AND NEW.revision=OLD.revision))
   OR (NOT OLD.dispatched AND NEW.dispatched AND NOT
    (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock intent, enablement history and leased progress are immutable';
  END IF;
 END IF;
 IF (NEW.enabled_required OR NEW.desired_snapshot<>'null' OR NOT NEW.observed_known) AND
  (NEW.state='applying' OR NEW.state='ready' AND (TG_OP='INSERT' OR OLD.state<>'ready')) AND
  (NOT object_lock_drained(NEW.bucket_id) OR NEW.enabled_required AND NOT object_lock_versioning_ready(NEW.bucket_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires drained transfers and verified Enabled versioning';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_bucket_object_lock_protected ON object_bucket_object_lock;
CREATE TRIGGER object_bucket_object_lock_protected BEFORE INSERT OR UPDATE OR DELETE ON object_bucket_object_lock
 FOR EACH ROW EXECUTE FUNCTION protect_object_bucket_object_lock();

-- Parent cascades must not bypass the permanent journal's deletion guard.
CREATE OR REPLACE FUNCTION protect_object_lock_bucket_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=OLD.id) THEN
  IF TG_OP='DELETE' THEN
   IF OLD.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock history requires confirmed physical bucket deletion'; END IF;
   RETURN OLD;
  END IF;
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.backend_id,NEW.backend_fingerprint,NEW.physical_name) IS DISTINCT FROM
   (OLD.id,OLD.account_id,OLD.app_id,OLD.backend_id,OLD.backend_fingerprint,OLD.physical_name) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock ownership and native placement are immutable';
  END IF;
  IF NEW.state='deleted' AND OLD.state<>'deleted' AND
   (OLD.state<>'deleting' OR OLD.lease_until IS NULL OR OLD.lease_until<=clock_timestamp()) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock bucket deletion requires its active native deletion lease';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lock_bucket_history_protected ON object_buckets;
CREATE TRIGGER object_lock_bucket_history_protected BEFORE UPDATE OR DELETE ON object_buckets
 FOR EACH ROW EXECUTE FUNCTION protect_object_lock_bucket_history();

CREATE OR REPLACE FUNCTION fence_object_lock_suspension() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
 IF NEW.desired_status='Suspended' AND EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND (enabled_required OR state<>'ready')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock permanently requires Enabled versioning';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lock_suspension_fence ON object_bucket_versioning;
CREATE TRIGGER object_lock_suspension_fence BEFORE INSERT OR UPDATE ON object_bucket_versioning FOR EACH ROW EXECUTE FUNCTION fence_object_lock_suspension();

CREATE OR REPLACE FUNCTION fence_object_lock_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF NEW.state<>'deleting' OR OLD.state='deleting' THEN RETURN NEW; END IF;
  bid:=NEW.id;
 ELSE
  IF TG_TABLE_NAME='object_upload_completions' THEN
   IF NEW.status='rejected' THEN RETURN NEW; END IF;
  END IF;
  bid:=NEW.bucket_id;
  PERFORM 1 FROM object_buckets WHERE id=bid FOR SHARE;
 END IF;
 IF TG_TABLE_NAME='object_storage_write_admissions' THEN
  -- An already accepted variable-size session can acquire its completion
  -- admission while the preceding native default is still in effect.
  IF NEW.kind='multipart' AND EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
   WHERE m.id=NEW.multipart_upload_id AND m.bucket_id=bid AND m.state NOT IN ('completed','aborted')) THEN RETURN NEW; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=bid AND state<>'ready') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lock_admission_fenced',MESSAGE='Unresolved Object Lock configuration fences new writes and bucket deletion';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lock_write_fence ON object_storage_write_admissions;
CREATE TRIGGER object_lock_write_fence BEFORE INSERT ON object_storage_write_admissions FOR EACH ROW EXECUTE FUNCTION fence_object_lock_admission();
DROP TRIGGER IF EXISTS object_lock_multipart_fence ON object_storage_multipart_uploads;
CREATE TRIGGER object_lock_multipart_fence BEFORE INSERT ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION fence_object_lock_admission();
DROP TRIGGER IF EXISTS object_lock_completion_fence ON object_upload_completions;
CREATE TRIGGER object_lock_completion_fence BEFORE INSERT ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION fence_object_lock_admission();
DROP TRIGGER IF EXISTS object_lock_key_grant_fence ON object_storage_key_grants;
CREATE TRIGGER object_lock_key_grant_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants FOR EACH ROW EXECUTE FUNCTION fence_object_lock_admission();
DROP TRIGGER IF EXISTS object_lock_bucket_deletion_fence ON object_buckets;
CREATE TRIGGER object_lock_bucket_deletion_fence BEFORE UPDATE ON object_buckets FOR EACH ROW EXECUTE FUNCTION fence_object_lock_admission();
CREATE OR REPLACE FUNCTION protect_object_bucket_versioning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.bucket_id<>OLD.bucket_id OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
   OR (NEW.revision<>OLD.revision AND OLD.state<>'ready' AND NOT (
    OLD.desired_status='Suspended' AND NEW.desired_status='Enabled' AND NEW.observed_status='Enabled'
    AND NEW.versions_required AND NOT NEW.dispatched AND NEW.lease_token='' AND NEW.lease_until IS NULL
    AND NEW.propagation_until>=clock_timestamp()+interval '14 minutes'
    AND (OLD.state<>'inventory' OR (NEW.state='inventory' AND NEW.capacity_job_id=OLD.capacity_job_id))
    AND EXISTS(SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=NEW.bucket_id AND l.native_enabled_observed)))
   OR (NEW.desired_status<>OLD.desired_status AND NEW.revision<>OLD.revision+1)
   OR (NEW.revision=OLD.revision AND OLD.dispatched AND NOT NEW.dispatched) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Versioning transition identity cannot change';
  END IF;
  NEW.versions_required:=OLD.versions_required OR NEW.versions_required;
 END IF;
 IF NEW.versions_required AND NEW.state='ready' AND (TG_OP='INSERT' OR OLD.state<>'ready') AND NOT EXISTS(
  SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.id=NEW.capacity_job_id AND c.bucket_id=NEW.bucket_id
  AND c.state='completed' AND c.inventory_scope='all_versions' AND c.inventory_verified AND c.created_at>=NEW.propagation_until AND NEW.propagation_until<=now()
 ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Versioning cutover requires propagated configuration and verified version inventory'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE enabled_required OR native_enabled_observed OR state<>'ready' OR desired_snapshot<>'null') THEN
  RAISE EXCEPTION 'Cannot discard Object Lock enablement, pending configuration or native protection history';
 END IF;
END $$;
DROP TRIGGER object_lock_bucket_history_protected ON object_buckets;
DROP FUNCTION protect_object_lock_bucket_history();
DROP TRIGGER object_lock_write_fence ON object_storage_write_admissions;
DROP TRIGGER object_lock_multipart_fence ON object_storage_multipart_uploads;
DROP TRIGGER object_lock_completion_fence ON object_upload_completions;
DROP TRIGGER object_lock_key_grant_fence ON object_storage_key_grants;
DROP TRIGGER object_lock_bucket_deletion_fence ON object_buckets;
DROP FUNCTION fence_object_lock_admission();
DROP TRIGGER object_lock_suspension_fence ON object_bucket_versioning;
DROP FUNCTION fence_object_lock_suspension();
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
DROP TABLE object_bucket_object_lock;
DROP FUNCTION protect_object_bucket_object_lock();
DROP FUNCTION object_lock_versioning_ready(uuid);
DROP FUNCTION object_lock_drained(uuid);
DROP FUNCTION valid_object_lock_configuration(jsonb);
DROP FUNCTION valid_object_lock_period(jsonb);
-- +goose StatementEnd
