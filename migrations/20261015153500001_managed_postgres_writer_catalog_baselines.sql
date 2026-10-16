-- +goose Up
ALTER TABLE managed_postgres_cutover_source_fences ADD COLUMN catalog_fingerprint text NOT NULL DEFAULT ''
 CHECK(catalog_fingerprint='' OR catalog_fingerprint ~ '^[a-f0-9]{64}$'),
 ADD COLUMN catalog_invalidated_at timestamptz;

-- +goose StatementBegin
CREATE FUNCTION guard_managed_postgres_writer_catalog_baseline() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND ((OLD.catalog_fingerprint<>'' AND NEW.catalog_fingerprint IS DISTINCT FROM OLD.catalog_fingerprint)
  OR (OLD.catalog_invalidated_at IS NOT NULL AND NEW.catalog_invalidated_at IS DISTINCT FROM OLD.catalog_invalidated_at)) THEN
  RAISE EXCEPTION 'original source writer catalog is retained' USING ERRCODE='55000';
 END IF;
 IF NEW.catalog_fingerprint<>'' AND (TG_OP='INSERT' OR OLD.catalog_fingerprint='') AND (
  NEW.state<>'closed' OR NEW.closure IS NULL OR
  NEW.closure->'writer_inventory'->>'catalog_fingerprint' IS DISTINCT FROM NEW.catalog_fingerprint OR
  NEW.closure->'writer_inventory'->>'catalog_complete' IS DISTINCT FROM 'true' OR
  NEW.closure->>'unselected_databases' IS DISTINCT FROM '0') THEN
  RAISE EXCEPTION 'original source writer catalog is incomplete' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER managed_postgres_writer_catalog_baseline BEFORE INSERT OR UPDATE ON managed_postgres_cutover_source_fences
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_writer_catalog_baseline();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_cutover_source_fences WHERE state<>'released' AND (catalog_fingerprint<>'' OR catalog_invalidated_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'active source writer catalog authority must be retained';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER managed_postgres_writer_catalog_baseline ON managed_postgres_cutover_source_fences;
DROP FUNCTION guard_managed_postgres_writer_catalog_baseline();
ALTER TABLE managed_postgres_cutover_source_fences DROP COLUMN catalog_invalidated_at,DROP COLUMN catalog_fingerprint;
