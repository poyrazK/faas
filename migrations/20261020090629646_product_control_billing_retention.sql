-- filename: 20261020090629646_product_control_billing_retention.sql

-- ADR-20261020090607: an app's Product Control receipts, finalized statements
-- and credit notes, and catalog revisions are kept when the app is purged, so
-- their waivers and invoice handoffs are kept too. They no longer cascade from
-- apps; their account and tenant foreign keys still erase them with the
-- account. Revisions stay because receipts reference them.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE product_control_billing_documents DROP CONSTRAINT IF EXISTS product_control_billing_documents_app_id_fkey;
ALTER TABLE product_control_receipts DROP CONSTRAINT IF EXISTS product_control_receipts_app_id_fkey;
ALTER TABLE product_control_revisions DROP CONSTRAINT IF EXISTS product_control_revisions_app_id_fkey;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Records retained for purged apps cannot reference them again.
DELETE FROM product_control_billing_documents d WHERE NOT EXISTS (SELECT 1 FROM apps a WHERE a.id = d.app_id);
DELETE FROM product_control_receipts r WHERE NOT EXISTS (SELECT 1 FROM apps a WHERE a.id = r.app_id);
DELETE FROM product_control_revisions v WHERE NOT EXISTS (SELECT 1 FROM apps a WHERE a.id = v.app_id);
ALTER TABLE product_control_revisions ADD CONSTRAINT product_control_revisions_app_id_fkey
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE;
ALTER TABLE product_control_receipts ADD CONSTRAINT product_control_receipts_app_id_fkey
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE;
ALTER TABLE product_control_billing_documents ADD CONSTRAINT product_control_billing_documents_app_id_fkey
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE;
-- +goose StatementEnd
