-- +goose Up
-- ADR-590: never infer that an existing initiating journal was not dispatched.
CREATE TABLE IF NOT EXISTS object_multipart_initiation_dispatches (
 multipart_upload_id uuid PRIMARY KEY REFERENCES object_storage_multipart_uploads(id) ON DELETE CASCADE,
 dispatched boolean NOT NULL DEFAULT false,
 dispatch_token text NOT NULL DEFAULT '',
 provider_upload_id text NOT NULL DEFAULT '',
 CHECK (dispatched OR (dispatch_token='' AND provider_upload_id='')),
 CHECK (octet_length(dispatch_token)<=128),
 CHECK (octet_length(provider_upload_id)<=4096)
);
DROP TRIGGER IF EXISTS object_multipart_initiation_dispatch_guard ON object_multipart_initiation_dispatches;
INSERT INTO object_multipart_initiation_dispatches(multipart_upload_id,dispatched,provider_upload_id)
SELECT u.id,true,u.provider_upload_id FROM object_storage_multipart_uploads u
JOIN object_bucket_mutations m ON m.multipart_upload_id=u.id
WHERE NOT EXISTS(SELECT 1 FROM object_multipart_initiation_dispatches d WHERE d.multipart_upload_id=u.id)
ON CONFLICT (multipart_upload_id) DO NOTHING;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_object_multipart_initiation_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id=OLD.multipart_upload_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_live',MESSAGE='Retain original initiation evidence while the provider receipt is outstanding';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_isolation',MESSAGE='Initiation dispatch requires READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
 PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
 IF u.id IS NULL OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NOT EXISTS(
  SELECT 1 FROM object_bucket_mutations m JOIN object_buckets b ON b.id=m.bucket_id
  WHERE m.multipart_upload_id=u.id AND m.kind='request' AND b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id
  AND b.state='ready' AND b.backend_id=m.backend_id AND b.backend_fingerprint=m.backend_fingerprint AND b.physical_name=m.physical_name
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_original',MESSAGE='Initiation requires the original session receipt and placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.dispatched OR NEW.dispatch_token<>'' OR NEW.provider_upload_id<>'' OR u.lease_token IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_reserved',MESSAGE='Initialize dispatch evidence only with a new reservation';
  END IF;
 ELSIF NEW.multipart_upload_id<>OLD.multipart_upload_id OR u.lease_token IS NULL OR u.lease_token='' OR u.lease_until IS NULL OR u.lease_until<=clock_timestamp() OR NOT (
  (NOT OLD.dispatched AND NEW.dispatched AND NEW.dispatch_token=u.lease_token AND NEW.provider_upload_id='') OR
  (OLD.dispatched AND NEW.dispatched AND OLD.dispatch_token=u.lease_token AND NEW.dispatch_token=OLD.dispatch_token AND OLD.provider_upload_id='' AND btrim(NEW.provider_upload_id)<>'' AND NEW.provider_upload_id !~ '[[:cntrl:]]')
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_immutable',MESSAGE='Dispatch is once-only and only its live owner may record a positive reply';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_multipart_initiation_dispatch_guard ON object_multipart_initiation_dispatches;
CREATE TRIGGER object_multipart_initiation_dispatch_guard BEFORE INSERT OR UPDATE OR DELETE ON object_multipart_initiation_dispatches FOR EACH ROW EXECUTE FUNCTION guard_object_multipart_initiation_dispatch();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compose_object_multipart_initiation_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.multipart_upload_id IS NOT NULL THEN
  INSERT INTO object_multipart_initiation_dispatches(multipart_upload_id) VALUES(NEW.multipart_upload_id);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_multipart_initiation_dispatch_composition ON object_bucket_mutations;
CREATE TRIGGER object_multipart_initiation_dispatch_composition AFTER INSERT ON object_bucket_mutations FOR EACH ROW EXECUTE FUNCTION compose_object_multipart_initiation_dispatch();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_multipart_initiation_result() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d object_multipart_initiation_dispatches%ROWTYPE;
BEGIN
 SELECT * INTO d FROM object_multipart_initiation_dispatches WHERE multipart_upload_id=OLD.id;
 IF d.multipart_upload_id IS NOT NULL AND OLD.state='initiating' AND (NEW.size_bytes IS DISTINCT FROM OLD.size_bytes OR NEW.part_count IS DISTINCT FROM OLD.part_count) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_intent',MESSAGE='Original initiation layout cannot change between authority checks and dispatch';
 END IF;
 IF d.dispatched AND d.dispatch_token<>'' AND NEW.provider_upload_id IS DISTINCT FROM OLD.provider_upload_id AND (
  OLD.state<>'initiating' OR NEW.state<>'active' OR OLD.lease_token IS NULL OR OLD.lease_until IS NULL OR OLD.lease_until<=clock_timestamp() OR d.provider_upload_id='' OR NEW.provider_upload_id<>d.provider_upload_id
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_positive_result',MESSAGE='Activate only the durably observed original initiation reply';
 END IF;
 IF d.dispatched AND d.provider_upload_id='' AND OLD.provider_upload_id='' AND NEW.state IN ('completed','aborted') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_uncertain',MESSAGE='An uncertain initiation cannot be settled by an empty listing or a timeout';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_multipart_initiation_result_guard ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_initiation_result_guard BEFORE UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_initiation_result();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id IS NOT NULL) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_down_busy',MESSAGE='Settle original multipart writers and release capture holds before removing dispatch evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_initiation_result_guard ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_initiation_result();
DROP TRIGGER object_multipart_initiation_dispatch_composition ON object_bucket_mutations;
DROP FUNCTION compose_object_multipart_initiation_dispatch();
DROP TRIGGER object_multipart_initiation_dispatch_guard ON object_multipart_initiation_dispatches;
DROP FUNCTION guard_object_multipart_initiation_dispatch();
DROP TABLE object_multipart_initiation_dispatches;
