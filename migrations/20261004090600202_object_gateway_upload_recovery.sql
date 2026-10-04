-- +goose Up
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'route' CHECK (origin IN ('route','gateway'));
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_gateway_receipt') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_gateway_receipt CHECK (origin<>'gateway' OR (route_id IS NULL AND idempotency_key='' AND request_fingerprint='' AND write_phase<>'untracked'));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_upload_completions WHERE origin='gateway' AND write_phase IN ('prepared','dispatched')) OR
 EXISTS (SELECT 1 FROM object_storage_write_admissions w JOIN object_upload_completions c ON c.id=w.id WHERE c.origin='gateway' AND w.state='pending') THEN
  RAISE EXCEPTION 'Settle gateway upload intents before rollback';
 END IF;
END $$;
-- +goose StatementEnd
UPDATE object_storage_key_grants g SET reclaimable=false,last_write_id=NULL
 WHERE EXISTS (SELECT 1 FROM object_upload_completions c WHERE c.id=g.last_write_id AND c.origin='gateway');
DELETE FROM object_storage_write_admissions w USING object_upload_completions c WHERE w.id=c.id AND c.origin='gateway';
DELETE FROM object_upload_completions WHERE origin='gateway';
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_gateway_receipt, DROP COLUMN origin;
