-- +goose Up
ALTER TABLE managed_postgres_cutover_source_fences ADD COLUMN IF NOT EXISTS catalog_fingerprint text NOT NULL DEFAULT ''
 CHECK(catalog_fingerprint='' OR catalog_fingerprint ~ '^[a-f0-9]{64}$'),
 ADD COLUMN IF NOT EXISTS catalog_invalidated_at timestamptz;

-- +goose StatementBegin
DO $migration$
BEGIN
 IF to_regprocedure('guard_managed_postgres_writer_catalog_baseline()') IS NULL THEN
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
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='managed_postgres_cutover_source_fences'::regclass AND tgname='managed_postgres_writer_catalog_baseline') THEN
CREATE TRIGGER managed_postgres_writer_catalog_baseline BEFORE INSERT OR UPDATE ON managed_postgres_cutover_source_fences
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_writer_catalog_baseline();
 END IF;
END;
$migration$;
-- +goose StatementEnd

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
