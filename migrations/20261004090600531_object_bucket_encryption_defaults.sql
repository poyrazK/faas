-- filename: 20261004090600531_object_bucket_encryption_defaults.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_bucket_encryption (
 bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
 account_id uuid NOT NULL, app_id uuid NOT NULL,
 state text NOT NULL CHECK (state IN ('waiting','applying','ready')),
 revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
 encryption_snapshot jsonb NOT NULL DEFAULT '{}', desired_snapshot jsonb NOT NULL DEFAULT '{}',
 lease_token text NOT NULL DEFAULT '' CHECK (octet_length(lease_token)<=128),
 lease_until timestamptz, retry_at timestamptz NOT NULL,
 dispatched boolean NOT NULL DEFAULT false, updated_at timestamptz NOT NULL,
 CHECK (valid_object_encryption_snapshot(encryption_snapshot,account_id) AND
  valid_object_encryption_snapshot(desired_snapshot,account_id) AND
  coalesce(encryption_snapshot->'selection'->>'context','')='' AND
  coalesce(desired_snapshot->'selection'->>'context','')=''),
 CHECK ((state='applying' AND lease_token<>'' AND lease_until IS NOT NULL) OR
  (state<>'applying' AND lease_token='' AND lease_until IS NULL)),
 CHECK (state<>'ready' OR encryption_snapshot=desired_snapshot)
);
CREATE INDEX IF NOT EXISTS object_bucket_encryption_due ON object_bucket_encryption(retry_at,bucket_id) WHERE state<>'ready';

CREATE OR REPLACE FUNCTION protect_object_bucket_encryption() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b object_buckets;
BEGIN
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id);
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state NOT IN ('deleting','deleted') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_encryption_fenced',MESSAGE='Clear policies without removing their revision tombstones';
  END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) OR b.state<>'ready' OR
  EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=b.id AND state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_encryption_fenced',MESSAGE='Encryption configuration requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.state<>'waiting' OR NEW.encryption_snapshot<>'{}' OR NEW.dispatched THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_encryption_fenced',MESSAGE='Encryption configuration requires a durable initial request';
  END IF;
  RETURN NEW;
 END IF;
 IF (NEW.bucket_id,NEW.account_id,NEW.app_id) IS DISTINCT FROM (OLD.bucket_id,OLD.account_id,OLD.app_id) OR
  NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1 OR
  (NEW.revision<>OLD.revision AND (OLD.state<>'ready' OR NEW.state<>'waiting' OR NEW.dispatched)) OR
  (NEW.revision=OLD.revision AND (NEW.desired_snapshot IS DISTINCT FROM OLD.desired_snapshot OR (OLD.dispatched AND NOT NEW.dispatched))) OR
  (NEW.encryption_snapshot IS DISTINCT FROM OLD.encryption_snapshot AND NOT
   (OLD.state='applying' AND OLD.lease_until>clock_timestamp() AND NEW.state='ready' AND NEW.revision=OLD.revision AND NEW.encryption_snapshot=OLD.desired_snapshot)) OR
  (OLD.state<>'ready' AND NEW.state='ready' AND NOT
   (OLD.state='applying' AND OLD.lease_until>clock_timestamp() AND NEW.revision=OLD.revision AND NEW.encryption_snapshot=OLD.desired_snapshot)) OR
  (NEW.state='applying' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token AND
   ((OLD.lease_until IS NOT NULL AND OLD.lease_until>clock_timestamp()) OR OLD.retry_at>clock_timestamp())) OR
  (NOT OLD.dispatched AND NEW.dispatched AND NOT (OLD.state='applying' AND NEW.state='applying' AND
   OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_encryption_fenced',MESSAGE='Encryption identity and leased configuration progress are immutable';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_bucket_encryption_immutable ON object_bucket_encryption;
CREATE TRIGGER object_bucket_encryption_immutable BEFORE INSERT OR UPDATE OR DELETE ON object_bucket_encryption
 FOR EACH ROW EXECUTE FUNCTION protect_object_bucket_encryption();

ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS encryption_default_revision bigint NOT NULL DEFAULT 0
 CHECK (encryption_default_revision BETWEEN 0 AND 9007199254740991 AND (encryption_default_revision=0 OR encryption_snapshot<>'{}'));
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS encryption_default_revision bigint NOT NULL DEFAULT 0
 CHECK (encryption_default_revision BETWEEN 0 AND 9007199254740991 AND (encryption_default_revision=0 OR encryption_snapshot<>'{}'));

CREATE OR REPLACE FUNCTION require_object_bucket_default(bucket uuid, snapshot jsonb, revision bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE p object_bucket_encryption;
BEGIN
 SELECT * INTO p FROM object_bucket_encryption WHERE bucket_id=bucket FOR SHARE;
 IF (revision>0 AND (NOT FOUND OR p.state<>'ready' OR p.revision<>revision OR p.encryption_snapshot IS DISTINCT FROM snapshot)) OR
  (revision=0 AND snapshot='{}' AND FOUND AND (p.state<>'ready' OR p.encryption_snapshot<>'{}')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_default_fenced',MESSAGE='New writes require the verified captured bucket default';
 END IF;
END $$;

CREATE OR REPLACE FUNCTION protect_object_default_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.encryption_default_revision IS DISTINCT FROM OLD.encryption_default_revision THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_default_fenced',MESSAGE='Captured default revisions are immutable';
  END IF;
 ELSE
  IF TG_TABLE_NAME<>'object_upload_completions' THEN
   PERFORM require_object_bucket_default(NEW.bucket_id,NEW.encryption_snapshot,NEW.encryption_default_revision);
  ELSIF NEW.status<>'rejected' THEN
   PERFORM require_object_bucket_default(NEW.bucket_id,NEW.encryption_snapshot,NEW.encryption_default_revision);
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_upload_default_bound ON object_upload_completions;
CREATE TRIGGER object_upload_default_bound BEFORE INSERT OR UPDATE ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION protect_object_default_snapshot();
DROP TRIGGER IF EXISTS object_multipart_default_bound ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_default_bound BEFORE INSERT OR UPDATE ON object_storage_multipart_uploads
 FOR EACH ROW EXECUTE FUNCTION protect_object_default_snapshot();

CREATE OR REPLACE FUNCTION protect_object_default_legacy_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NULL THEN PERFORM require_object_bucket_default(NEW.bucket_id,'{}',0); END IF;
 ELSE
  IF NEW.kind='proxy' AND NOT NEW.route_receipt THEN PERFORM require_object_bucket_default(NEW.bucket_id,'{}',0); END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_default_key_grant_bound ON object_storage_key_grants;
CREATE TRIGGER object_default_key_grant_bound BEFORE INSERT ON object_storage_key_grants
 FOR EACH ROW EXECUTE FUNCTION protect_object_default_legacy_write();
DROP TRIGGER IF EXISTS object_default_write_admission_bound ON object_storage_write_admissions;
CREATE TRIGGER object_default_write_admission_bound BEFORE INSERT ON object_storage_write_admissions
 FOR EACH ROW EXECUTE FUNCTION protect_object_default_legacy_write();

CREATE OR REPLACE FUNCTION protect_object_route_encryption_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r object_upload_routes;
BEGIN
 IF NEW.route_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO r FROM object_upload_routes WHERE id=NEW.route_id FOR SHARE;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF (NEW.write_phase='prepared' AND
   ((r.encryption_snapshot<>'{}' AND (NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot OR NEW.encryption_default_revision<>0)) OR
    (r.encryption_snapshot='{}' AND NEW.encryption_snapshot<>'{}' AND NEW.encryption_default_revision=0))) OR
  (r.encryption_snapshot<>'{}' AND NEW.status<>'rejected' AND
   ((NEW.account_id,NEW.app_id,NEW.bucket_id) IS DISTINCT FROM (r.account_id,r.app_id,r.bucket_id) OR
    NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot OR NEW.encryption_default_revision<>0 OR NEW.write_phase<>'prepared' OR NEW.status<>'pending')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_route_encryption_fenced',MESSAGE='Route writes require the current captured encryption policy';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION protect_object_bucket_encryption_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state<>OLD.state AND NEW.state='deleting' AND EXISTS(SELECT 1 FROM object_bucket_encryption WHERE bucket_id=OLD.id AND state<>'ready') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_encryption_fenced',MESSAGE='Drain encryption configuration before deleting a bucket';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_bucket_encryption_deletion_bound ON object_buckets;
CREATE TRIGGER object_bucket_encryption_deletion_bound BEFORE UPDATE ON object_buckets
 FOR EACH ROW EXECUTE FUNCTION protect_object_bucket_encryption_deletion();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_encryption WHERE state<>'ready' OR encryption_snapshot<>'{}' OR desired_snapshot<>'{}') OR
  EXISTS(SELECT 1 FROM object_upload_completions WHERE encryption_default_revision>0 AND write_phase IN ('prepared','dispatched')) OR
  EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE encryption_default_revision>0 AND state NOT IN ('completed','aborted')) THEN
  RAISE EXCEPTION 'Clear bucket defaults and drain their configuration and write journals before rollback';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION protect_object_route_encryption_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r object_upload_routes;
BEGIN
 IF NEW.route_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO r FROM object_upload_routes WHERE id=NEW.route_id FOR SHARE;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF (NEW.write_phase='prepared' AND NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot) OR
  (r.encryption_snapshot<>'{}' AND NEW.status<>'rejected' AND
   ((NEW.account_id,NEW.app_id,NEW.bucket_id) IS DISTINCT FROM (r.account_id,r.app_id,r.bucket_id) OR
    NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot OR NEW.write_phase<>'prepared' OR NEW.status<>'pending')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_route_encryption_fenced',MESSAGE='Route writes require the current captured encryption policy';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER object_bucket_encryption_deletion_bound ON object_buckets;
DROP FUNCTION protect_object_bucket_encryption_deletion();
DROP TRIGGER object_default_key_grant_bound ON object_storage_key_grants;
DROP TRIGGER object_default_write_admission_bound ON object_storage_write_admissions;
DROP FUNCTION protect_object_default_legacy_write();
DROP TRIGGER object_upload_default_bound ON object_upload_completions;
DROP TRIGGER object_multipart_default_bound ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_default_snapshot();
DROP FUNCTION require_object_bucket_default(uuid,jsonb,bigint);
ALTER TABLE object_upload_completions DROP COLUMN encryption_default_revision;
ALTER TABLE object_storage_multipart_uploads DROP COLUMN encryption_default_revision;
DROP TABLE object_bucket_encryption;
DROP FUNCTION protect_object_bucket_encryption();
-- +goose StatementEnd
