-- filename: 20261002205414508_financial_usage_adjustments.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-530: corrections preserve originals, terms and workload identity.
ALTER TABLE financial_usage_evidence ADD COLUMN IF NOT EXISTS corrects_source_id text CHECK (corrects_source_id IS NULL OR length(corrects_source_id) BETWEEN 1 AND 512);
ALTER TABLE financial_usage_evidence ADD COLUMN IF NOT EXISTS adjustment_actor text CHECK (adjustment_actor IS NULL OR octet_length(adjustment_actor) BETWEEN 1 AND 256);
ALTER TABLE financial_usage_evidence ADD COLUMN IF NOT EXISTS adjustment_reason text CHECK (adjustment_reason IS NULL OR octet_length(adjustment_reason) BETWEEN 1 AND 512);
ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_evidence_quantity_check;
ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_adjustment_quantity_check;
ALTER TABLE financial_usage_evidence ADD CONSTRAINT financial_usage_adjustment_quantity_check CHECK (
  (quantity > 0 AND corrects_source_id IS NULL AND adjustment_actor IS NULL AND adjustment_reason IS NULL)
  OR (quantity < 0 AND corrects_source_id IS NOT NULL AND corrects_source_id != source_id AND adjustment_actor IS NOT NULL AND adjustment_reason IS NOT NULL)
);
ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_adjustment_original_fk;
ALTER TABLE financial_usage_evidence ADD CONSTRAINT financial_usage_adjustment_original_fk FOREIGN KEY (account_id, corrects_source_id)
  REFERENCES financial_usage_evidence(account_id, source_id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX IF NOT EXISTS financial_usage_adjustment_original_idx ON financial_usage_evidence(account_id, corrects_source_id) WHERE corrects_source_id IS NOT NULL;

-- Allocate sequence inside the account lock, including internal/manual source
-- adapters. A DEFAULT sequence allocated before a BEFORE trigger could commit
-- behind the fixed read head and violate snapshot paging.
ALTER TABLE financial_usage_evidence ALTER COLUMN id DROP DEFAULT;
CREATE OR REPLACE FUNCTION validate_financial_evidence_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  original financial_usage_evidence%ROWTYPE;
  existing financial_usage_evidence%ROWTYPE;
  remaining numeric;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('financial-evidence:' || NEW.account_id::text, 0));
  NEW.id := nextval('financial_usage_evidence_id_seq'::regclass);
  IF NEW.corrects_source_id IS NULL THEN RETURN NEW; END IF;
  SELECT * INTO existing FROM financial_usage_evidence WHERE account_id = NEW.account_id AND source_id = NEW.source_id;
  IF FOUND THEN
    IF (NEW.instance_id,NEW.corrects_source_id,NEW.meter,NEW.unit,NEW.quantity,NEW.source_start,NEW.source_end,NEW.plan,NEW.attribution,NEW.price_version,NEW.adjustment_actor,NEW.adjustment_reason)
       IS DISTINCT FROM (existing.instance_id,existing.corrects_source_id,existing.meter,existing.unit,existing.quantity,existing.source_start,existing.source_end,existing.plan,existing.attribution,existing.price_version,existing.adjustment_actor,existing.adjustment_reason) THEN
      RAISE EXCEPTION 'conflicting financial adjustment replay' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO original FROM financial_usage_evidence WHERE account_id = NEW.account_id AND source_id = NEW.corrects_source_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'financial correction original not found' USING ERRCODE = '23503'; END IF;
  IF original.corrects_source_id IS NOT NULL OR NEW.quantity >= 0
    OR (NEW.instance_id,NEW.meter,NEW.unit,NEW.source_start,NEW.source_end,NEW.plan,NEW.attribution,NEW.price_version)
       IS DISTINCT FROM (original.instance_id,original.meter,original.unit,original.source_start,original.source_end,original.plan,original.attribution,original.price_version) THEN
    RAISE EXCEPTION 'invalid financial correction lineage' USING ERRCODE = '23514';
  END IF;
  SELECT original.quantity::numeric + COALESCE(sum(quantity::numeric),0) + NEW.quantity::numeric INTO remaining
    FROM financial_usage_evidence WHERE account_id = NEW.account_id AND corrects_source_id = NEW.corrects_source_id;
  IF remaining < 0 THEN RAISE EXCEPTION 'financial correction exceeds original usage' USING ERRCODE = '23514'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS financial_evidence_insert_guard ON financial_usage_evidence;
CREATE TRIGGER financial_evidence_insert_guard BEFORE INSERT ON financial_usage_evidence FOR EACH ROW EXECUTE FUNCTION validate_financial_evidence_insert();

CREATE OR REPLACE FUNCTION protect_financial_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  IF TG_TABLE_NAME = 'financial_price_snapshots' AND TG_OP = 'UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'financial history is append-only';
END $$;
DROP TRIGGER IF EXISTS financial_evidence_immutable ON financial_usage_evidence;
CREATE TRIGGER financial_evidence_immutable BEFORE UPDATE OR DELETE ON financial_usage_evidence FOR EACH ROW EXECUTE FUNCTION protect_financial_history();
DROP TRIGGER IF EXISTS financial_price_immutable ON financial_price_snapshots;
CREATE TRIGGER financial_price_immutable BEFORE UPDATE OR DELETE ON financial_price_snapshots FOR EACH ROW EXECUTE FUNCTION protect_financial_history();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A rollback that would erase corrections is forbidden; disable consumers and
-- retain accounting evidence before selecting a schema rollback.
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM financial_usage_evidence WHERE corrects_source_id IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot remove financial corrections with retained adjustments';
  END IF;
END $$;
DROP TRIGGER IF EXISTS financial_evidence_insert_guard ON financial_usage_evidence;
DROP TRIGGER IF EXISTS financial_evidence_immutable ON financial_usage_evidence;
DROP TRIGGER IF EXISTS financial_price_immutable ON financial_price_snapshots;
DROP FUNCTION IF EXISTS validate_financial_evidence_insert();
DROP FUNCTION IF EXISTS protect_financial_history();
ALTER TABLE financial_usage_evidence ALTER COLUMN id SET DEFAULT nextval('financial_usage_evidence_id_seq'::regclass);
ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_adjustment_original_fk;
ALTER TABLE financial_usage_evidence DROP CONSTRAINT IF EXISTS financial_usage_adjustment_quantity_check;
DROP INDEX IF EXISTS financial_usage_adjustment_original_idx;
ALTER TABLE financial_usage_evidence DROP COLUMN IF EXISTS corrects_source_id;
ALTER TABLE financial_usage_evidence DROP COLUMN IF EXISTS adjustment_actor;
ALTER TABLE financial_usage_evidence DROP COLUMN IF EXISTS adjustment_reason;
ALTER TABLE financial_usage_evidence ADD CONSTRAINT financial_usage_evidence_quantity_check CHECK(quantity > 0);
-- +goose StatementEnd
