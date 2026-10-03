-- filename: 20261003001442856_object_lifecycle_deletion_binding.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_deletions ADD COLUMN lifecycle_scan_id uuid REFERENCES object_lifecycle_scans(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE object_deletions ADD COLUMN lifecycle_binding jsonb NOT NULL DEFAULT '{}';
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
CREATE INDEX object_deletion_lifecycle_scan ON object_deletions(lifecycle_scan_id,object_key,id) WHERE lifecycle_scan_id IS NOT NULL;

CREATE FUNCTION fence_object_lifecycle_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion identity is immutable';
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT (OLD.state='prepared' AND NEW.state='dispatched') THEN RETURN NEW; END IF;
 -- Both the API store and this defensive trigger follow bucket-before-account
 -- lock order. Re-read the lease and revision after acquiring those locks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion requires a ready owned bucket'; END IF;
 PERFORM 1 FROM accounts WHERE id=aid FOR UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning'
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND ((NEW.lifecycle_binding->>'kind'='current' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date'))
    OR (NEW.lifecycle_binding->>'kind'='noncurrent' AND r ? 'noncurrent_version_expiration')
    OR (NEW.lifecycle_binding->>'kind'='expired_marker' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date' OR r->'expiration'->>'expired_object_delete_marker'='true')))
 ) INTO allowed;
 IF NOT allowed OR (NEW.lifecycle_binding->>'expected_last_modified')::timestamptz>clock_timestamp()
  OR (NEW.selector='null' AND NEW.lifecycle_binding->>'expected_provider_version_id'<>'null')
  OR (NEW.selector NOT IN ('','null') AND NEW.lifecycle_binding->>'expected_provider_version_id'<>coalesce(to_jsonb(NEW)->>'target_provider_version_id','')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_deletion_fenced',MESSAGE='A live matching lifecycle scan and target are required before dispatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_lifecycle_deletion_fence BEFORE INSERT OR UPDATE ON object_deletions FOR EACH ROW EXECUTE FUNCTION fence_object_lifecycle_deletion();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_deletions WHERE lifecycle_scan_id IS NOT NULL) THEN RAISE EXCEPTION 'Cannot discard lifecycle deletion identities or receipts'; END IF;
END $$;
DROP TRIGGER object_lifecycle_deletion_fence ON object_deletions;
DROP FUNCTION fence_object_lifecycle_deletion();
DROP INDEX object_deletion_lifecycle_scan;
ALTER TABLE object_deletions DROP CONSTRAINT object_deletion_lifecycle_binding;
ALTER TABLE object_deletions DROP COLUMN lifecycle_binding;
ALTER TABLE object_deletions DROP COLUMN lifecycle_scan_id;
-- +goose StatementEnd
