-- +goose Up
-- Initial boot receipts cannot authorize a later boot on the same row. Warm
-- promotion and migration need their own fresh attempt protocol; cleanup stays
-- possible. There is no historical receipt backfill or compatibility grant.
-- +goose StatementBegin
CREATE FUNCTION application_standard_boot_receipt_reuse_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IN ('waking','cold_booting') AND EXISTS(
  SELECT 1 FROM instance_application_standard_boots WHERE instance_id=NEW.id AND receipt IS NOT NULL
 ) THEN
  RAISE EXCEPTION 'published native receipt cannot authorize another boot'
   USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_b1_boot_receipt_reuse BEFORE INSERT OR UPDATE OF state ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_boot_receipt_reuse_guard();

-- +goose Down
-- Retain replay protection for already admitted runtimes.
