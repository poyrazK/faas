-- filename: 20261005143206150_object_lifecycle_protection.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_deletions ADD COLUMN IF NOT EXISTS protection_required boolean NOT NULL DEFAULT false;
ALTER TABLE object_deletions ADD COLUMN IF NOT EXISTS protection_verified boolean NOT NULL DEFAULT false;
ALTER TABLE object_deletions ADD COLUMN IF NOT EXISTS deletion_verified boolean NOT NULL DEFAULT false;

-- Historical active attempts retain all original custody and dispatch evidence.
-- Missing historical marker classification deliberately cannot be invented.
UPDATE object_deletions d SET protection_required=true
FROM object_bucket_object_lock l WHERE l.bucket_id=d.bucket_id
 AND d.lifecycle_scan_id IS NOT NULL AND d.selector<>'' AND d.state IN ('prepared','dispatched')
 AND NOT d.protection_required AND (l.enabled_required OR l.native_enabled_observed OR l.observed_snapshot->>'enabled'='true')
 AND NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='object_deletions'::regclass AND tgname='object_deletion_protection_fence');

ALTER TABLE object_deletions DROP CONSTRAINT IF EXISTS object_deletion_lifecycle_binding;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletion_lifecycle_binding CHECK(
 (lifecycle_scan_id IS NULL AND lifecycle_binding='{}') OR
 (lifecycle_scan_id IS NOT NULL AND jsonb_typeof(lifecycle_binding)='object'
  AND lifecycle_binding ?& ARRAY['scan_id','scan_token','rule_id','kind','expected_provider_version_id','expected_last_modified']
  AND lifecycle_binding - ARRAY['scan_id','scan_token','rule_id','kind','expected_provider_version_id','expected_last_modified','expected_delete_marker']='{}'
  AND (NOT lifecycle_binding ? 'expected_delete_marker' OR jsonb_typeof(lifecycle_binding->'expected_delete_marker')='boolean')
  AND lifecycle_binding->>'scan_id'=lifecycle_scan_id::text
  AND jsonb_typeof(lifecycle_binding->'scan_token')='string' AND octet_length(lifecycle_binding->>'scan_token') BETWEEN 1 AND 128
  AND jsonb_typeof(lifecycle_binding->'rule_id')='string' AND char_length(lifecycle_binding->>'rule_id') BETWEEN 1 AND 255
  AND jsonb_typeof(lifecycle_binding->'expected_provider_version_id')='string' AND octet_length(lifecycle_binding->>'expected_provider_version_id') BETWEEN 1 AND 1024
  AND jsonb_typeof(lifecycle_binding->'expected_last_modified')='string' AND lifecycle_binding->>'expected_last_modified'<>''
  AND octet_length(lifecycle_binding::text)<=8192
  AND ((lifecycle_binding->>'kind'='current' AND selector='') OR (lifecycle_binding->>'kind' IN ('noncurrent','expired_marker') AND selector<>'')))
);
ALTER TABLE object_deletions DROP CONSTRAINT IF EXISTS object_deletions_last_error_code_check;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_last_error_code_check CHECK(last_error_code IN ('','provider_uncertain','configuration','preparation_failed','preparation_expired','provider_rejected','object_protected'));
ALTER TABLE object_deletions DROP CONSTRAINT IF EXISTS object_deletions_check2;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_check2 CHECK(state<>'failed' OR last_error_code IN ('preparation_failed','preparation_expired','provider_rejected','object_protected'));
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='object_deletions'::regclass AND conname='object_deletion_protection_proof') THEN
  ALTER TABLE object_deletions ADD CONSTRAINT object_deletion_protection_proof CHECK(
   (NOT protection_required OR lifecycle_scan_id IS NOT NULL AND selector<>'')
   AND (NOT protection_verified OR protection_required AND state IN ('dispatched','completed','failed'))
   AND (NOT deletion_verified OR protection_required AND state='completed')
   AND (NOT protection_required OR state<>'completed' OR deletion_verified)
   AND (last_error_code<>'object_protected' OR protection_required AND state='failed')
  );
 END IF;
END $$;

-- Separate from historical lifecycle/deletion functions: replaying an older
-- migration cannot remove the policy and absence-proof requirements.
CREATE OR REPLACE FUNCTION fence_object_lifecycle_protection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE aid uuid; required boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.protection_verified OR NEW.deletion_verified OR NEW.protection_required AND NEW.state<>'prepared' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle intents must begin prepared and unverified';
  END IF;
  SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id;
  PERFORM 1 FROM accounts WHERE id=aid FOR UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT NEW.lifecycle_scan_id IS NOT NULL AND NEW.selector<>'' AND EXISTS(
   SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=NEW.bucket_id
    AND (l.enabled_required OR l.native_enabled_observed OR l.observed_snapshot->>'enabled'='true')
  ) INTO required;
  IF NEW.protection_required IS DISTINCT FROM required OR required AND jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle deletion requires a frozen target classification';
  END IF;
 ELSE
  IF NEW.protection_required<>OLD.protection_required OR OLD.protection_verified AND NOT NEW.protection_verified OR OLD.deletion_verified AND NOT NEW.deletion_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle protection evidence is immutable';
  END IF;
  IF NOT OLD.protection_verified AND NEW.protection_verified AND NOT (NEW.protection_required AND OLD.state='prepared' AND NEW.state='dispatched') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection proof must accompany initial dispatch';
  END IF;
  IF NOT OLD.deletion_verified AND NEW.deletion_verified AND NOT (OLD.state='dispatched' AND NEW.state='completed') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Absence proof must accompany completion';
  END IF;
  IF NEW.last_error_code='object_protected' AND NOT (OLD.state='prepared' AND NEW.state='failed') AND OLD.last_error_code<>'object_protected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='A dispatched uncertain attempt cannot be dismissed as protected';
  END IF;
  IF OLD.state='prepared' AND NEW.state='dispatched' AND NEW.protection_required AND NOT NEW.protection_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Fresh native policy proof is required before lifecycle dispatch';
  END IF;
 END IF;
 IF NEW.protection_required AND NEW.state='completed' AND (
  NOT NEW.deletion_verified OR jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean'
  OR NEW.delete_marker::text IS DISTINCT FROM NEW.lifecycle_binding->>'expected_delete_marker'
  OR NEW.provider_version_id IS DISTINCT FROM NEW.lifecycle_binding->>'expected_provider_version_id') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Exact lifecycle target absence proof is required before completion';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_deletion_protection_fence ON object_deletions;
CREATE TRIGGER object_deletion_protection_fence BEFORE INSERT OR UPDATE ON object_deletions FOR EACH ROW EXECUTE FUNCTION fence_object_lifecycle_protection();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_deletions WHERE protection_required OR protection_verified OR deletion_verified OR lifecycle_binding ? 'expected_delete_marker' OR last_error_code='object_protected') THEN
  RAISE EXCEPTION 'Cannot discard lifecycle protection identities, proofs or receipts';
 END IF;
END $$;
DROP TRIGGER object_deletion_protection_fence ON object_deletions;
DROP FUNCTION fence_object_lifecycle_protection();
ALTER TABLE object_deletions DROP CONSTRAINT object_deletion_protection_proof;
ALTER TABLE object_deletions DROP CONSTRAINT object_deletion_lifecycle_binding;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletion_lifecycle_binding CHECK(
 (lifecycle_scan_id IS NULL AND lifecycle_binding='{}') OR
 (lifecycle_scan_id IS NOT NULL AND jsonb_typeof(lifecycle_binding)='object'
  AND lifecycle_binding ?& ARRAY['scan_id','scan_token','rule_id','kind','expected_provider_version_id','expected_last_modified']
  AND lifecycle_binding - ARRAY['scan_id','scan_token','rule_id','kind','expected_provider_version_id','expected_last_modified']='{}'
  AND lifecycle_binding->>'scan_id'=lifecycle_scan_id::text
  AND jsonb_typeof(lifecycle_binding->'scan_token')='string' AND octet_length(lifecycle_binding->>'scan_token') BETWEEN 1 AND 128
  AND jsonb_typeof(lifecycle_binding->'rule_id')='string' AND char_length(lifecycle_binding->>'rule_id') BETWEEN 1 AND 255
  AND jsonb_typeof(lifecycle_binding->'expected_provider_version_id')='string' AND octet_length(lifecycle_binding->>'expected_provider_version_id') BETWEEN 1 AND 1024
  AND jsonb_typeof(lifecycle_binding->'expected_last_modified')='string' AND lifecycle_binding->>'expected_last_modified'<>''
  AND octet_length(lifecycle_binding::text)<=8192
  AND ((lifecycle_binding->>'kind'='current' AND selector='') OR (lifecycle_binding->>'kind' IN ('noncurrent','expired_marker') AND selector<>'')))
);
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_last_error_code_check;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_last_error_code_check CHECK(last_error_code IN ('','provider_uncertain','configuration','preparation_failed','preparation_expired','provider_rejected'));
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_check2;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_check2 CHECK(state<>'failed' OR last_error_code IN ('preparation_failed','preparation_expired','provider_rejected'));
ALTER TABLE object_deletions DROP COLUMN deletion_verified;
ALTER TABLE object_deletions DROP COLUMN protection_verified;
ALTER TABLE object_deletions DROP COLUMN protection_required;
-- +goose StatementEnd
