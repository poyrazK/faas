-- filename: 20261004090600424_object_multipart_signed_part_fence.sql

-- +goose Up
-- Old fixed-size sessions did not journal issued URL deadlines. Preserve their
-- reservations through the session lifetime, a maximum 15-minute URL, the
-- 30-minute transfer timeout and five-minute cleanup grace.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='object_storage_multipart_uploads'::regclass AND attname='part_url_unsafe_until' AND NOT attisdropped) THEN
  ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS part_url_unsafe_until timestamptz;
  UPDATE object_storage_multipart_uploads
SET part_url_unsafe_until=greatest(expires_at,clock_timestamp())+interval '50 minutes'
WHERE part_count>0 AND state IN ('initiating','active','completing','completing_conditional','aborting');
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_multipart_uploads'::regclass AND conname='object_multipart_part_url_deadline_shape') THEN
  ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_part_url_deadline_shape CHECK(part_url_unsafe_until IS NULL OR (part_count>0 AND part_url_unsafe_until>=created_at));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_multipart_part_url_deadline() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.part_url_unsafe_until IS DISTINCT FROM OLD.part_url_unsafe_until
  AND (OLD.state<>'active' OR NEW.state<>'active' OR NEW.part_url_unsafe_until IS NULL
   OR (OLD.part_url_unsafe_until IS NOT NULL AND NEW.part_url_unsafe_until<OLD.part_url_unsafe_until)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart part URL cleanup deadline cannot be shortened or changed after admission';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_multipart_part_url_deadline_protected ON object_storage_multipart_uploads;
CREATE TRIGGER object_multipart_part_url_deadline_protected BEFORE UPDATE ON object_storage_multipart_uploads
FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_part_url_deadline();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE part_url_unsafe_until IS NOT NULL) THEN
  RAISE EXCEPTION 'Cannot discard multipart part URL cleanup fences';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_part_url_deadline_protected ON object_storage_multipart_uploads;
DROP FUNCTION protect_object_multipart_part_url_deadline();
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_part_url_deadline_shape,
 DROP COLUMN part_url_unsafe_until;
