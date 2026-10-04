-- filename: 20261004090600223_object_gateway_copy_recovery.sql

-- +goose Up
ALTER TABLE object_upload_completions
 DROP CONSTRAINT object_upload_completions_origin_check,
 DROP CONSTRAINT object_upload_gateway_receipt,
 ADD COLUMN source_key text NOT NULL DEFAULT '',
 ADD COLUMN source_etag text NOT NULL DEFAULT '',
 ADD CONSTRAINT object_upload_completions_origin_check CHECK (origin IN ('route','gateway','gateway_copy')),
 ADD CONSTRAINT object_upload_gateway_receipt CHECK (origin NOT IN ('gateway','gateway_copy') OR (route_id IS NULL AND idempotency_key='' AND request_fingerprint='' AND write_phase<>'untracked')),
 ADD CONSTRAINT object_upload_copy_source CHECK ((origin='gateway_copy' AND length(source_key) BETWEEN 1 AND 1024 AND length(source_etag) BETWEEN 1 AND 256 AND btrim(source_etag)<>'' AND position(chr(10) IN source_etag)=0 AND position(chr(13) IN source_etag)=0) OR (origin<>'gateway_copy' AND source_key='' AND source_etag=''));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_upload_completions WHERE origin='gateway_copy' AND write_phase IN ('prepared','dispatched')) OR
 EXISTS (SELECT 1 FROM object_storage_write_admissions w JOIN object_upload_completions c ON c.id=w.id WHERE c.origin='gateway_copy' AND w.state='pending') THEN
  RAISE EXCEPTION 'Settle gateway copy intents before rollback';
 END IF;
END $$;
-- +goose StatementEnd
UPDATE object_storage_key_grants g SET reclaimable=false,last_write_id=NULL
 WHERE EXISTS (SELECT 1 FROM object_upload_completions c WHERE c.id=g.last_write_id AND c.origin='gateway_copy');
DELETE FROM object_storage_write_admissions w USING object_upload_completions c WHERE w.id=c.id AND c.origin='gateway_copy';
DELETE FROM object_upload_completions WHERE origin='gateway_copy';
ALTER TABLE object_upload_completions
 DROP CONSTRAINT object_upload_copy_source,
 DROP CONSTRAINT object_upload_completions_origin_check,
 DROP CONSTRAINT object_upload_gateway_receipt,
 DROP COLUMN source_key,
 DROP COLUMN source_etag,
 ADD CONSTRAINT object_upload_completions_origin_check CHECK (origin IN ('route','gateway')),
 ADD CONSTRAINT object_upload_gateway_receipt CHECK (origin<>'gateway' OR (route_id IS NULL AND idempotency_key='' AND request_fingerprint='' AND write_phase<>'untracked'));
