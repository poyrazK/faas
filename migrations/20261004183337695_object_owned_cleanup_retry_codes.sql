-- filename: 20261004183337695_object_owned_cleanup_retry_codes.sql

-- +goose Up
ALTER TABLE object_buckets DROP CONSTRAINT object_buckets_last_error_code_check;
ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_last_error_code_check
 CHECK (last_error_code IN ('', 'temporary', 'configuration', 'conflict', 'invalid', 'protected', 'cleanup_pending'));

-- An expired account cannot regain provider resources through an older writer.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_bucket_account_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_status text;
BEGIN
 SELECT status INTO current_status FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 IF current_status IS DISTINCT FROM 'active' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_account_cleanup_fenced',MESSAGE='Inactive account cannot reserve new object buckets';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_bucket_account_cleanup_fence ON object_buckets;
CREATE TRIGGER object_bucket_account_cleanup_fence BEFORE INSERT ON object_buckets
 FOR EACH ROW EXECUTE FUNCTION fence_object_bucket_account_cleanup();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_buckets WHERE state='deleting' OR last_error_code IN ('protected','cleanup_pending')) THEN
  RAISE EXCEPTION 'Finish owned bucket cleanup before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_buckets DROP CONSTRAINT object_buckets_last_error_code_check;
ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_last_error_code_check
 CHECK (last_error_code IN ('', 'temporary', 'configuration', 'conflict', 'invalid'));
DROP TRIGGER object_bucket_account_cleanup_fence ON object_buckets;
DROP FUNCTION fence_object_bucket_account_cleanup();
