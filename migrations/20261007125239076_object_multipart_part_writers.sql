-- +goose Up
-- ADR-590: reserve attempt identity with the transfer, claim before IO, and
-- retain uncertain dispatch forever. Existing transfers are never adopted.
CREATE TABLE IF NOT EXISTS object_multipart_part_writers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 upload_id uuid NOT NULL REFERENCES object_storage_multipart_uploads(id) ON DELETE CASCADE,
 part_number integer NOT NULL CHECK(part_number BETWEEN 1 AND 10000),
 transfer_token text NOT NULL CHECK(octet_length(transfer_token) BETWEEN 1 AND 128),
 managed boolean NOT NULL DEFAULT true,
 dispatched boolean NOT NULL DEFAULT false,
 settled boolean NOT NULL DEFAULT false,
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE RESTRICT,
 backend_id text NOT NULL,
 backend_fingerprint text NOT NULL,
 physical_name text NOT NULL,
 UNIQUE(upload_id,part_number,transfer_token),
 CHECK(managed OR dispatched)
);
DROP TRIGGER IF EXISTS object_multipart_part_writer_guard ON object_multipart_part_writers;
INSERT INTO object_multipart_part_writers(upload_id,part_number,transfer_token,managed,dispatched,bucket_id,backend_id,backend_fingerprint,physical_name)
SELECT g.upload_id,g.part_number,g.transfer_token,false,true,b.id,b.backend_id,b.backend_fingerprint,b.physical_name
FROM object_storage_multipart_part_grants g JOIN object_storage_multipart_uploads u ON u.id=g.upload_id JOIN object_buckets b ON b.id=u.bucket_id
WHERE g.transfer_token IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM object_multipart_part_writers d WHERE d.upload_id=g.upload_id AND d.part_number=g.part_number AND d.transfer_token=g.transfer_token)
ON CONFLICT (upload_id,part_number,transfer_token) DO NOTHING;
ALTER TABLE object_bucket_mutations ADD COLUMN IF NOT EXISTS multipart_part_writer_id uuid UNIQUE REFERENCES object_multipart_part_writers(id) ON DELETE RESTRICT;
ALTER TABLE object_bucket_mutations DROP CONSTRAINT object_mutation_single_owner;
ALTER TABLE object_bucket_mutations ADD CONSTRAINT object_mutation_single_owner CHECK(num_nonnulls(upload_id,multipart_upload_id,multipart_part_writer_id)<=1);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_object_multipart_part_writer() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE; g object_storage_multipart_part_grants%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.dispatched AND NOT OLD.settled THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain part evidence cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writers require READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
 PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 SELECT * INTO g FROM object_storage_multipart_part_grants WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number;
 IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id AND b.state='ready' AND b.backend_id=NEW.backend_id AND b.backend_fingerprint=NEW.backend_fingerprint AND b.physical_name=NEW.physical_name) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writer requires its original placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_part_capture_fenced',MESSAGE='Capture fences new part transfer admission';
  END IF;
  IF NOT NEW.managed OR NEW.dispatched OR NEW.settled OR u.state<>'active' OR g.transfer_token IS DISTINCT FROM NEW.transfer_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Reserve part evidence only with new unfenced transfer authority';
  END IF;
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Dispatch once; settle only the original attempt with qualified proof';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_writer_guard ON object_multipart_part_writers;
CREATE TRIGGER object_multipart_part_writer_guard BEFORE INSERT OR UPDATE OR DELETE ON object_multipart_part_writers FOR EACH ROW EXECUTE FUNCTION guard_object_multipart_part_writer();

CREATE OR REPLACE FUNCTION guard_object_multipart_part_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.multipart_part_writer_id IS NOT NULL AND EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE id=OLD.multipart_part_writer_id AND NOT settled) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Settle the original part writer before deleting its receipt';
  END IF;
  RETURN OLD;
 ELSIF TG_OP='UPDATE' THEN
  IF NEW.multipart_part_writer_id IS NOT NULL OR OLD.multipart_part_writer_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Original part receipts are immutable';
  END IF;
 ELSIF NEW.multipart_part_writer_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM object_multipart_part_writers d WHERE d.id=NEW.multipart_part_writer_id AND d.id=NEW.id AND d.managed AND d.dispatched AND NOT d.settled AND d.bucket_id=NEW.bucket_id AND d.backend_id=NEW.backend_id AND d.backend_fingerprint=NEW.backend_fingerprint AND d.physical_name=NEW.physical_name AND NEW.kind='request') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Bind the receipt to its original claimed part attempt';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_receipt_guard ON object_bucket_mutations;
CREATE TRIGGER object_multipart_part_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON object_bucket_mutations FOR EACH ROW EXECUTE FUNCTION guard_object_multipart_part_receipt();

CREATE OR REPLACE FUNCTION compose_object_multipart_part_writer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.dispatched AND NOT OLD.dispatched THEN
  INSERT INTO object_bucket_mutations(id,bucket_id,kind,backend_id,backend_fingerprint,physical_name,multipart_part_writer_id)
  VALUES(NEW.id,NEW.bucket_id,'request',NEW.backend_id,NEW.backend_fingerprint,NEW.physical_name,NEW.id);
 ELSIF NEW.settled AND NOT OLD.settled THEN
  UPDATE object_storage_multipart_part_grants SET transfer_token=NULL,unsafe_until=NULL WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number AND transfer_token=NEW.transfer_token;
  DELETE FROM object_bucket_mutations WHERE multipart_part_writer_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_writer_composition ON object_multipart_part_writers;
CREATE TRIGGER object_multipart_part_writer_composition AFTER UPDATE ON object_multipart_part_writers FOR EACH ROW EXECUTE FUNCTION compose_object_multipart_part_writer();

CREATE OR REPLACE FUNCTION protect_object_multipart_part_transfer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' AND EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE upload_id=OLD.upload_id AND part_number=OLD.part_number AND transfer_token=OLD.transfer_token AND dispatched AND NOT settled) AND (TG_OP='DELETE' OR NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='An uncertain dispatched transfer cannot expire, change or be replaced';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_transfer_guard ON object_storage_multipart_part_grants;
CREATE TRIGGER object_multipart_part_transfer_guard BEFORE UPDATE OR DELETE ON object_storage_multipart_part_grants FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_part_transfer();

CREATE OR REPLACE FUNCTION compose_object_multipart_part_transfer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' AND OLD.transfer_token IS NOT NULL AND (TG_OP='DELETE' OR NEW.transfer_token IS DISTINCT FROM OLD.transfer_token) THEN
  UPDATE object_multipart_part_writers SET settled=true WHERE upload_id=OLD.upload_id AND part_number=OLD.part_number AND transfer_token=OLD.transfer_token AND NOT dispatched AND NOT settled;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.transfer_token IS NOT NULL AND (TG_OP='INSERT' OR NEW.transfer_token IS DISTINCT FROM OLD.transfer_token) THEN
  INSERT INTO object_multipart_part_writers(upload_id,part_number,transfer_token,bucket_id,backend_id,backend_fingerprint,physical_name)
  SELECT NEW.upload_id,NEW.part_number,NEW.transfer_token,b.id,b.backend_id,b.backend_fingerprint,b.physical_name FROM object_storage_multipart_uploads u JOIN object_buckets b ON b.id=u.bucket_id WHERE u.id=NEW.upload_id;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_transfer_composition ON object_storage_multipart_part_grants;
CREATE TRIGGER object_multipart_part_transfer_composition AFTER INSERT OR UPDATE OR DELETE ON object_storage_multipart_part_grants FOR EACH ROW EXECUTE FUNCTION compose_object_multipart_part_transfer();

CREATE OR REPLACE FUNCTION protect_object_multipart_part_session() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.state IN ('completed','aborted') AND EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE upload_id=OLD.id AND dispatched AND NOT settled)) OR (EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE upload_id=OLD.id AND NOT settled) AND (
  (NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.provider_upload_id,NEW.encryption_snapshot,NEW.protection_snapshot) IS DISTINCT FROM
  (OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.provider_upload_id,OLD.encryption_snapshot,OLD.protection_snapshot)
 )) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain independent writers retain their original parent intent';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_session_guard ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_part_session_guard BEFORE UPDATE ON object_storage_multipart_uploads FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_part_session();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_multipart_part_writers WHERE dispatched AND NOT settled) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Drain independent writers and release capture holds before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_part_session_guard ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_part_session();
DROP TRIGGER object_multipart_part_transfer_composition ON object_storage_multipart_part_grants;
DROP FUNCTION compose_object_multipart_part_transfer();
DROP TRIGGER object_multipart_part_transfer_guard ON object_storage_multipart_part_grants;
DROP FUNCTION protect_object_multipart_part_transfer();
DROP TRIGGER object_multipart_part_writer_composition ON object_multipart_part_writers;
DROP FUNCTION compose_object_multipart_part_writer();
DROP TRIGGER object_multipart_part_receipt_guard ON object_bucket_mutations;
DROP FUNCTION guard_object_multipart_part_receipt();
DROP TRIGGER object_multipart_part_writer_guard ON object_multipart_part_writers;
DROP FUNCTION guard_object_multipart_part_writer();
ALTER TABLE object_bucket_mutations DROP CONSTRAINT object_mutation_single_owner, DROP COLUMN multipart_part_writer_id;
ALTER TABLE object_bucket_mutations ADD CONSTRAINT object_mutation_single_owner CHECK(upload_id IS NULL OR multipart_upload_id IS NULL);
DROP TABLE object_multipart_part_writers;
