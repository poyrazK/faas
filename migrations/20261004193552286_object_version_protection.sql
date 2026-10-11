-- filename: 20261004193552286_object_version_protection.sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_version_protection (
 id uuid PRIMARY KEY CHECK(id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 object_key text NOT NULL CHECK(octet_length(object_key) BETWEEN 1 AND 1024 AND object_key !~ '[\x01-\x1f\x7f]'),
 public_version_id text NOT NULL CHECK(public_version_id='null' OR public_version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 native_version_id text NOT NULL CHECK(octet_length(native_version_id) BETWEEN 1 AND 1024 AND native_version_id !~ '[\x01-\x1f\x7f]'),
 intent jsonb NOT NULL CHECK(jsonb_typeof(intent)='object' AND octet_length(intent::text)<=32768 AND coalesce(intent->>'kind' IN ('retention','legal_hold'),false)),
 state text NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','applying','ready','failed')),
 lease_token text NOT NULL DEFAULT '' CHECK(octet_length(lease_token)<=128),
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT now(),
 dispatched boolean NOT NULL DEFAULT false,
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code IN ('','provider_uncertain','provider_unsupported','provider_mismatch','preparation_failed','provider_rejected')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((state='applying')=(lease_token<>'' AND lease_until IS NOT NULL)),
 CHECK(state='applying' OR lease_token='' AND lease_until IS NULL),
 CHECK((public_version_id='null')=(native_version_id='null')),
 CHECK(coalesce(intent->>'id'=id::text AND intent->>'bucket_id'=bucket_id::text AND intent->>'key'=object_key AND intent->>'version_id'=public_version_id,false)),
 CHECK(coalesce((intent->>'kind'='legal_hold' AND NOT intent ? 'retention' AND jsonb_typeof(intent->'legal_hold')='object' AND intent->'legal_hold'->>'status' IN ('ON','OFF')) OR
 (intent->>'kind'='retention' AND NOT intent ? 'legal_hold' AND jsonb_typeof(intent->'retention')='object' AND NOT intent->'retention' ? 'event_hold' AND NOT intent->'retention' ? 'event_hold_duration' AND
 (intent->'retention'='{}'::jsonb OR (intent->'retention'->>'mode' IN ('GOVERNANCE','COMPLIANCE') AND jsonb_typeof(intent->'retention'->'retain_until_date')='string'))),false))
);
CREATE UNIQUE INDEX IF NOT EXISTS object_version_protection_active ON object_version_protection(bucket_id) WHERE state IN ('waiting','applying');
CREATE INDEX IF NOT EXISTS object_version_protection_due ON object_version_protection(retry_at,id) WHERE state IN ('waiting','applying');

CREATE OR REPLACE FUNCTION protect_object_version_protection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b object_buckets; owner uuid;
BEGIN
 -- The admission/cascade boundary uses account-before-bucket locking.
 SELECT account_id INTO owner FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id);
 PERFORM 1 FROM accounts WHERE id=owner FOR UPDATE;
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve protection receipts until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR b.state<>'ready' OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'waiting' OR NEW.dispatched OR NOT object_lock_versioning_ready(NEW.bucket_id) OR NOT object_lock_drained(NEW.bucket_id)
   OR NOT EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND state='ready' AND observed_known AND native_enabled_observed AND observed_snapshot->>'enabled'='true')
   OR EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=NEW.bucket_id AND state IN ('prepared','dispatched'))
   OR EXISTS(SELECT 1 FROM object_bucket_encryption WHERE bucket_id=NEW.bucket_id AND state<>'ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires verified Object Lock and drained mutations';
  END IF;
  IF NEW.public_version_id<>'null' AND NOT EXISTS(SELECT 1 FROM object_version_references WHERE id=NEW.public_version_id::uuid AND bucket_id=NEW.bucket_id AND object_key=NEW.object_key AND native_version_id=NEW.native_version_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned exact version';
  END IF;
 ELSE
  IF (NEW.id,NEW.bucket_id,NEW.account_id,NEW.app_id,NEW.object_key,NEW.public_version_id,NEW.native_version_id,NEW.intent,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.bucket_id,OLD.account_id,OLD.app_id,OLD.object_key,OLD.public_version_id,OLD.native_version_id,OLD.intent,OLD.created_at)
   OR OLD.state IN ('ready','failed') OR OLD.dispatched AND NOT NEW.dispatched
   OR NEW.state='applying' AND NEW.lease_token<>OLD.lease_token AND (OLD.lease_until>clock_timestamp() OR OLD.retry_at>clock_timestamp())
   OR NOT OLD.dispatched AND NEW.dispatched AND NOT (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())
   OR NEW.state IN ('ready','failed') AND NOT (OLD.state='applying' AND OLD.lease_until>clock_timestamp())
   OR NEW.state='failed' AND OLD.dispatched AND NEW.last_error_code<>'provider_rejected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection intent and leased progress are immutable';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_version_protection_guard ON object_version_protection;
CREATE TRIGGER object_version_protection_guard BEFORE INSERT OR UPDATE OR DELETE ON object_version_protection FOR EACH ROW EXECUTE FUNCTION protect_object_version_protection();

CREATE OR REPLACE FUNCTION fence_object_version_protection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bid uuid;
BEGIN
 IF TG_TABLE_NAME='object_buckets' THEN
  IF NEW.state<>'deleting' OR OLD.state='deleting' THEN RETURN NEW; END IF;
  bid:=NEW.id;
 ELSE
  bid:=NEW.bucket_id;
  IF TG_OP='UPDATE' AND TG_TABLE_NAME IN ('object_bucket_object_lock','object_bucket_encryption','object_bucket_versioning') THEN
   IF NEW.revision=OLD.revision AND NOT (NOT OLD.dispatched AND NEW.dispatched) THEN RETURN NEW; END IF;
  END IF;
  PERFORM 1 FROM object_buckets WHERE id=bid FOR SHARE;
 END IF;
 IF EXISTS(SELECT 1 FROM object_version_protection WHERE bucket_id=bid AND state IN ('waiting','applying')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_version_protection_fenced',MESSAGE='Unsettled version protection fences writes, deletion and configuration';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS version_protection_delete_fence ON object_deletions;
CREATE TRIGGER version_protection_delete_fence BEFORE INSERT ON object_deletions FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_bucket_fence ON object_buckets;
CREATE TRIGGER version_protection_bucket_fence BEFORE UPDATE ON object_buckets FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_capacity_fence ON object_storage_capacity_reconciliations;
CREATE TRIGGER version_protection_capacity_fence BEFORE INSERT ON object_storage_capacity_reconciliations FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_write_fence ON object_storage_write_admissions;
CREATE TRIGGER version_protection_write_fence BEFORE INSERT ON object_storage_write_admissions FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_upload_fence ON object_upload_completions;
CREATE TRIGGER version_protection_upload_fence BEFORE INSERT ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_multipart_fence ON object_storage_multipart_uploads;
CREATE TRIGGER version_protection_multipart_fence BEFORE INSERT ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_grant_fence ON object_storage_key_grants;
CREATE TRIGGER version_protection_grant_fence BEFORE INSERT OR UPDATE ON object_storage_key_grants FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_lock_fence ON object_bucket_object_lock;
CREATE TRIGGER version_protection_lock_fence BEFORE INSERT OR UPDATE ON object_bucket_object_lock FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_encryption_fence ON object_bucket_encryption;
CREATE TRIGGER version_protection_encryption_fence BEFORE INSERT OR UPDATE ON object_bucket_encryption FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
DROP TRIGGER IF EXISTS version_protection_versioning_fence ON object_bucket_versioning;
CREATE TRIGGER version_protection_versioning_fence BEFORE INSERT OR UPDATE ON object_bucket_versioning FOR EACH ROW EXECUTE FUNCTION fence_object_version_protection();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_version_protection) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve version protection receipts before rollback'; END IF;
END $$;
DROP TRIGGER version_protection_delete_fence ON object_deletions;
DROP TRIGGER version_protection_bucket_fence ON object_buckets;
DROP TRIGGER version_protection_capacity_fence ON object_storage_capacity_reconciliations;
DROP TRIGGER version_protection_write_fence ON object_storage_write_admissions;
DROP TRIGGER version_protection_upload_fence ON object_upload_completions;
DROP TRIGGER version_protection_multipart_fence ON object_storage_multipart_uploads;
DROP TRIGGER version_protection_grant_fence ON object_storage_key_grants;
DROP TRIGGER version_protection_lock_fence ON object_bucket_object_lock;
DROP TRIGGER version_protection_encryption_fence ON object_bucket_encryption;
DROP TRIGGER version_protection_versioning_fence ON object_bucket_versioning;
DROP FUNCTION fence_object_version_protection();
DROP TABLE object_version_protection;
DROP FUNCTION protect_object_version_protection();
-- +goose StatementEnd
