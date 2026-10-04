-- filename: 20261004090600424_object_multipart_signed_part_fence.sql

-- +goose Up
ALTER TABLE object_storage_multipart_uploads ADD COLUMN part_url_unsafe_until timestamptz,
 ADD CONSTRAINT object_multipart_part_url_deadline_shape CHECK(part_url_unsafe_until IS NULL OR (part_count>0 AND part_url_unsafe_until>=created_at));
-- Old fixed-size sessions did not journal issued URL deadlines. Preserve their
-- reservations through the session lifetime, a maximum 15-minute URL, the
-- 30-minute transfer timeout and five-minute cleanup grace.
UPDATE object_storage_multipart_uploads
SET part_url_unsafe_until=greatest(expires_at,clock_timestamp())+interval '50 minutes'
WHERE part_count>0 AND state IN ('initiating','active','completing','completing_conditional','aborting');

-- +goose StatementBegin
CREATE FUNCTION protect_object_multipart_part_url_deadline() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.part_url_unsafe_until IS DISTINCT FROM OLD.part_url_unsafe_until
  AND (OLD.state<>'active' OR NEW.state<>'active' OR NEW.part_url_unsafe_until IS NULL
   OR (OLD.part_url_unsafe_until IS NOT NULL AND NEW.part_url_unsafe_until<OLD.part_url_unsafe_until)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart part URL cleanup deadline cannot be shortened or changed after admission';
 END IF;
 RETURN NEW;
END $$;
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
