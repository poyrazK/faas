-- +goose Up
-- adr: 393. Private complete shared-drive scan evidence, not runtime approval.
CREATE TABLE base_image_scans (
 id uuid PRIMARY KEY,
 base_producer_id uuid NOT NULL,
 storage_key text NOT NULL CHECK (storage_key ~ '^base/[^/]+[.]ext4$' AND length(storage_key)<=512),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 result_snapshot jsonb NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
 scanned_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at>scanned_at AND expires_at<=scanned_at+interval '5 minutes'),
 UNIQUE(id,storage_key),
 FOREIGN KEY(base_producer_id,storage_key) REFERENCES base_image_producers(id,storage_key),
 CHECK (input_snapshot->>'base_producer_id'=base_producer_id::text AND input_snapshot->'artifact'->>'storage_key'=storage_key),
 CHECK (input_snapshot->>'status' IN ('complete','failed') AND result_snapshot->>'status'=input_snapshot->>'status')
);
CREATE TABLE base_image_scan_current (
 storage_key text PRIMARY KEY CHECK (storage_key ~ '^base/[^/]+[.]ext4$' AND length(storage_key)<=512),
 scan_id uuid NOT NULL,
 FOREIGN KEY(scan_id,storage_key) REFERENCES base_image_scans(id,storage_key)
);
-- +goose StatementBegin
CREATE FUNCTION base_image_scan_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.base_scan_insert',true)=NEW.id::text THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'base scan evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='base_image_scan_immutable';
END;
$$;
CREATE FUNCTION base_image_scan_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'DELETE' AND current_setting('gregale.base_scan_insert',true)=NEW.scan_id::text
   AND (TG_OP='INSERT' OR NEW.storage_key=OLD.storage_key) THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'base scan selection is private' USING ERRCODE='23514',CONSTRAINT='base_image_scan_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER base_scan_private_guard BEFORE INSERT OR UPDATE OR DELETE ON base_image_scans FOR EACH ROW EXECUTE FUNCTION base_image_scan_guard();
CREATE TRIGGER base_scan_current_private_guard BEFORE INSERT OR UPDATE OR DELETE ON base_image_scan_current FOR EACH ROW EXECUTE FUNCTION base_image_scan_current_guard();
-- +goose Down
DROP TABLE base_image_scan_current;
DROP TABLE base_image_scans;
DROP FUNCTION base_image_scan_current_guard();
DROP FUNCTION base_image_scan_guard();
