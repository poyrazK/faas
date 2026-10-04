-- filename: 20261004090600520_object_fixed_multipart_admission.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS fixed_admission boolean NOT NULL DEFAULT false
 CHECK (NOT fixed_admission OR (size_bytes>0 AND part_size_bytes>0 AND part_count>0 AND part_count=(size_bytes+part_size_bytes-1)/part_size_bytes));

CREATE OR REPLACE FUNCTION protect_fixed_multipart_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.fixed_admission AND OLD.state NOT IN ('completed','aborted') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_fixed_multipart_admission_fenced',MESSAGE='Live fixed multipart sessions cannot be removed';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.fixed_admission IS DISTINCT FROM OLD.fixed_admission OR
  (OLD.fixed_admission AND (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.size_bytes,NEW.part_size_bytes,NEW.part_count)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.size_bytes,OLD.part_size_bytes,OLD.part_count)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_fixed_multipart_admission_fenced',MESSAGE='Fixed multipart admission identity and layout are immutable';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_fixed_multipart_admission_immutable ON object_storage_multipart_uploads;
CREATE TRIGGER object_fixed_multipart_admission_immutable BEFORE UPDATE OR DELETE ON object_storage_multipart_uploads
 FOR EACH ROW EXECUTE FUNCTION protect_fixed_multipart_admission();

CREATE OR REPLACE FUNCTION require_fixed_multipart_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads;
BEGIN
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.id;
 IF NOT FOUND OR NOT u.fixed_admission OR u.state IN ('completed','aborted') THEN RETURN NULL; END IF;
 IF NOT EXISTS(SELECT 1 FROM object_storage_write_admissions w WHERE w.id=u.id AND w.multipart_upload_id=u.id AND w.bucket_id=u.bucket_id
  AND w.kind='multipart' AND NOT w.route_receipt AND w.key_hash=encode(sha256(convert_to(u.object_key,'UTF8')),'hex')
  AND w.native_bytes=CASE WHEN w.native_version THEN u.size_bytes ELSE 0 END) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_fixed_multipart_admission_fenced',MESSAGE='Fixed multipart session requires its full-object admission';
 END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS object_fixed_multipart_admission_bound ON object_storage_multipart_uploads;
CREATE CONSTRAINT TRIGGER object_fixed_multipart_admission_bound AFTER INSERT OR UPDATE ON object_storage_multipart_uploads
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.fixed_admission AND NEW.state NOT IN ('completed','aborted'))
 EXECUTE FUNCTION require_fixed_multipart_admission();

CREATE OR REPLACE FUNCTION preserve_fixed_multipart_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads m WHERE m.id=OLD.id AND m.fixed_admission AND m.state NOT IN ('completed','aborted')) THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_fixed_multipart_admission_fenced',MESSAGE='Live fixed multipart capacity cannot be removed';
  END IF;
  IF NEW IS DISTINCT FROM OLD THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_fixed_multipart_admission_fenced',MESSAGE='Live fixed multipart capacity is immutable';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_fixed_multipart_capacity_immutable ON object_storage_write_admissions;
CREATE TRIGGER object_fixed_multipart_capacity_immutable BEFORE UPDATE OR DELETE ON object_storage_write_admissions
 FOR EACH ROW EXECUTE FUNCTION preserve_fixed_multipart_capacity();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE fixed_admission AND state NOT IN ('completed','aborted')) THEN
  RAISE EXCEPTION 'Drain fixed multipart admissions before rollback';
 END IF;
END $$;
DROP TRIGGER object_fixed_multipart_admission_bound ON object_storage_multipart_uploads;
DROP TRIGGER object_fixed_multipart_admission_immutable ON object_storage_multipart_uploads;
DROP TRIGGER object_fixed_multipart_capacity_immutable ON object_storage_write_admissions;
DROP FUNCTION require_fixed_multipart_admission();
DROP FUNCTION protect_fixed_multipart_admission();
DROP FUNCTION preserve_fixed_multipart_capacity();
ALTER TABLE object_storage_multipart_uploads DROP COLUMN fixed_admission;
-- +goose StatementEnd
