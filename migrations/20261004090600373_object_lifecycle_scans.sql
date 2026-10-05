-- +goose Up
CREATE TABLE IF NOT EXISTS object_bucket_lifecycle (
 bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=1000),
 next_scan_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS object_lifecycle_scans (
 id uuid PRIMARY KEY,
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules) BETWEEN 1 AND 1000),
 state text NOT NULL DEFAULT 'scanning' CHECK(state IN ('scanning','completed','cancelled')),
 last_key text NOT NULL DEFAULT '' CHECK(octet_length(last_key)<=1024),
 scanned_keys bigint NOT NULL DEFAULT 0 CHECK(scanned_keys>=0),
 lease_token text NOT NULL DEFAULT '' CHECK(octet_length(lease_token)<=128),
 lease_until timestamptz,
 retry_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 finished_at timestamptz,
 CHECK((lease_token='')=(lease_until IS NULL)),
 CHECK((state='scanning')=(finished_at IS NULL)),
 CHECK(state='scanning' OR lease_until IS NULL),
 CHECK(updated_at>=created_at),
 CHECK(finished_at IS NULL OR finished_at>=created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS object_lifecycle_active_scan ON object_lifecycle_scans(bucket_id) WHERE state='scanning';
CREATE INDEX IF NOT EXISTS object_lifecycle_scan_due ON object_lifecycle_scans(retry_at,bucket_id) WHERE state='scanning';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_lifecycle_scan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.revision<>OLD.revision
   OR NEW.rules<>OLD.rules OR NEW.created_at<>OLD.created_at
   OR NEW.scanned_keys<OLD.scanned_keys OR NEW.scanned_keys>OLD.scanned_keys+1
   OR (NEW.last_key IS DISTINCT FROM OLD.last_key AND (NEW.last_key COLLATE "C"<=OLD.last_key COLLATE "C" OR NEW.scanned_keys<>OLD.scanned_keys+1))
   OR (NEW.last_key=OLD.last_key AND NEW.scanned_keys<>OLD.scanned_keys)
   OR (OLD.state<>'scanning' AND NEW IS DISTINCT FROM OLD) THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle scan identity and progress cannot be rewritten';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lifecycle_scan_protected ON object_lifecycle_scans;
CREATE TRIGGER object_lifecycle_scan_protected BEFORE UPDATE ON object_lifecycle_scans FOR EACH ROW EXECUTE FUNCTION protect_object_lifecycle_scan();
CREATE OR REPLACE FUNCTION protect_object_lifecycle_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.bucket_id<>OLD.bucket_id OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
  OR (NEW.rules<>OLD.rules AND NEW.revision<>OLD.revision+1)
  OR (NEW.revision<>OLD.revision AND EXISTS(SELECT 1 FROM object_lifecycle_scans WHERE bucket_id=NEW.bucket_id AND state='scanning' AND lease_until>clock_timestamp())) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle policy identity or a live scan lease prevents replacement';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_lifecycle_policy_protected ON object_bucket_lifecycle;
CREATE TRIGGER object_lifecycle_policy_protected BEFORE UPDATE ON object_bucket_lifecycle FOR EACH ROW EXECUTE FUNCTION protect_object_lifecycle_policy();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_lifecycle) OR EXISTS(SELECT 1 FROM object_lifecycle_scans) THEN
  RAISE EXCEPTION 'Cannot discard lifecycle policy or scan history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE object_lifecycle_scans;
DROP TABLE object_bucket_lifecycle;
DROP FUNCTION protect_object_lifecycle_scan();
DROP FUNCTION protect_object_lifecycle_policy();
