-- filename: 20261004090600511_object_upload_route_encryption.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_upload_routes ADD COLUMN IF NOT EXISTS encryption_snapshot jsonb NOT NULL DEFAULT '{}'
 CHECK (valid_object_encryption_snapshot(encryption_snapshot,account_id));

CREATE OR REPLACE FUNCTION protect_object_route_encryption_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r object_upload_routes;
BEGIN
 IF NEW.route_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO r FROM object_upload_routes WHERE id=NEW.route_id FOR SHARE;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF (NEW.write_phase='prepared' AND NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot) OR
  (r.encryption_snapshot<>'{}' AND NEW.status<>'rejected' AND
   ((NEW.account_id,NEW.app_id,NEW.bucket_id) IS DISTINCT FROM (r.account_id,r.app_id,r.bucket_id) OR
    NEW.encryption_snapshot IS DISTINCT FROM r.encryption_snapshot OR NEW.write_phase<>'prepared' OR NEW.status<>'pending')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_route_encryption_fenced',MESSAGE='Route writes require the current captured encryption policy';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_route_encryption_receipt_bound ON object_upload_completions;
CREATE TRIGGER object_route_encryption_receipt_bound BEFORE INSERT ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION protect_object_route_encryption_receipt();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_upload_routes WHERE encryption_snapshot<>'{}') OR
 EXISTS(SELECT 1 FROM object_upload_completions WHERE origin='route' AND encryption_snapshot<>'{}' AND write_phase IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION 'Clear encrypted route policies and drain their pending receipts before rollback';
 END IF;
END $$;
DROP TRIGGER object_route_encryption_receipt_bound ON object_upload_completions;
DROP FUNCTION protect_object_route_encryption_receipt();
ALTER TABLE object_upload_routes DROP COLUMN encryption_snapshot;
-- +goose StatementEnd
