-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1748. An attestation is incarnation-bound and retained on old registration.
ALTER TABLE financial_compute_owners ADD COLUMN IF NOT EXISTS egress_budget_incarnation uuid;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_owners'::regclass AND conname='financial_egress_owner_incarnation') THEN
  ALTER TABLE financial_compute_owners ADD CONSTRAINT financial_egress_owner_incarnation
 CHECK (egress_budget_incarnation IS NULL OR egress_budget_incarnation=incarnation);
 END IF;
END $$;

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_account_books'::regclass AND conname='financial_egress_book_bounds') THEN
  ALTER TABLE financial_compute_account_books ADD CONSTRAINT financial_egress_book_bounds CHECK (
 NOT COALESCE((payload->'egress'->>'initialized')::boolean,false) OR
 COALESCE((payload->'egress'->>'quantity_committed')::numeric>=0
 AND (payload->'egress'->>'quantity_reserved')::numeric>=0
 AND (payload->'egress'->>'quantity_committed')::numeric+(payload->'egress'->>'quantity_reserved')::numeric<=9007199254740991
 AND (payload->'egress'->>'allowance_granted')::numeric BETWEEN 0 AND 9007199254740991
 AND (payload->'egress'->>'committed')::numeric>=0 AND (payload->'egress'->>'reserved')::numeric>=0
 AND (payload->'egress'->>'committed')::numeric+(payload->'egress'->>'reserved')::numeric<=9007199254740991
 AND (payload->'egress'->>'rate_millicents')::numeric>=0
 AND (payload->'egress'->>'rate_quantity')::numeric>0,false));
 END IF;
END $$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_leases'::regclass AND conname='financial_egress_lease_bounds') THEN
  ALTER TABLE financial_compute_leases ADD CONSTRAINT financial_egress_lease_bounds CHECK (
 NOT COALESCE((payload->>'egress_funded')::boolean,false) OR
 COALESCE(strict AND (payload->>'account_funded')::boolean
 AND (payload->>'egress_granted_bytes')::numeric BETWEEN 0 AND 9007199254740991
 AND (payload->>'egress_grant_bytes')::numeric BETWEEN 0 AND (payload->>'egress_granted_bytes')::numeric
 AND (payload->>'egress_sequence')::numeric BETWEEN 0 AND 9007199254740991,false));
 END IF;
END $$;

CREATE OR REPLACE FUNCTION financial_egress_require_funding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 IF NEW.settled_at IS NULL AND EXISTS(SELECT 1 FROM financial_budget_policies p
  WHERE p.account_id=NEW.account_id AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND p.spec->'scope'->>'kind'='account' AND p.spec->'meters' ? 'egress') THEN
  IF NOT COALESCE((NEW.payload->>'egress_funded')::boolean,false)
   OR NOT EXISTS(SELECT 1 FROM financial_compute_owners o JOIN compute_nodes n ON n.id=o.node_id
    WHERE o.node_id=NEW.node_id AND o.incarnation=NEW.incarnation
    AND o.egress_budget_incarnation=NEW.incarnation AND n.vmmd_incarnation=NEW.incarnation AND n.active) THEN
   RAISE EXCEPTION 'strict egress requires funded byte-capable owner' USING ERRCODE='23514',CONSTRAINT='financial_egress_owner_funded';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER financial_egress_require_funding BEFORE INSERT OR UPDATE ON financial_compute_leases
 FOR EACH ROW EXECUTE FUNCTION financial_egress_require_funding();

-- Price insertion shares the account lock, including legacy COPY writers.
-- A later contract cannot reprice already sent bytes beyond their funding.
CREATE OR REPLACE FUNCTION financial_egress_price_ceiling() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b jsonb;
BEGIN
 IF NEW.meter!='egress' THEN RETURN NEW; END IF;
 PERFORM 1 FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 SELECT payload->'egress' INTO b FROM financial_compute_account_books
 WHERE account_id=NEW.account_id AND period_start=NEW.period_start;
 IF COALESCE((b->>'initialized')::boolean,false) AND
  (NEW.price->>'currency' IS DISTINCT FROM 'EUR' OR NEW.price->>'unit' IS DISTINCT FROM 'interface_bytes'
   OR NOT COALESCE((NEW.price->>'unit_quantity')::numeric>0 AND (NEW.price->>'millicents_per_unit')::numeric>=0
    AND (NEW.price->>'millicents_per_unit')::numeric*(b->>'rate_quantity')::numeric
     <=(b->>'rate_millicents')::numeric*(NEW.price->>'unit_quantity')::numeric,false)) THEN
  RAISE EXCEPTION 'egress price exceeds funded period ceiling' USING ERRCODE='23514',CONSTRAINT='financial_egress_price_ceiling';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER financial_egress_price_ceiling BEFORE INSERT OR UPDATE ON financial_price_snapshots
 FOR EACH ROW EXECUTE FUNCTION financial_egress_price_ceiling();

-- A legacy book writer must not discard byte fields after policy removal.
-- Issued quantities are never refunded, and the period price ceiling is pinned.
CREATE OR REPLACE FUNCTION financial_egress_preserve_book() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_egress jsonb := OLD.payload->'egress'; new_egress jsonb := NEW.payload->'egress';
BEGIN
 IF COALESCE((old_egress->>'initialized')::boolean,false) AND NOT COALESCE(
  (new_egress->>'initialized')::boolean
  AND (new_egress->>'quantity_committed')::numeric>=(old_egress->>'quantity_committed')::numeric
  AND (new_egress->>'quantity_committed')::numeric+(new_egress->>'quantity_reserved')::numeric
   >=(old_egress->>'quantity_committed')::numeric+(old_egress->>'quantity_reserved')::numeric
  AND (new_egress->>'allowance_granted')::numeric>=(old_egress->>'allowance_granted')::numeric
  AND (new_egress->>'committed')::numeric>=(old_egress->>'committed')::numeric
  AND new_egress->'rate_millicents'=old_egress->'rate_millicents'
  AND new_egress->'rate_quantity'=old_egress->'rate_quantity',false) THEN
  RAISE EXCEPTION 'egress book authority cannot be discarded or reduced' USING ERRCODE='23514',CONSTRAINT='financial_egress_book_monotonic';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER financial_egress_preserve_book BEFORE UPDATE ON financial_compute_account_books
 FOR EACH ROW EXECUTE FUNCTION financial_egress_preserve_book();

CREATE OR REPLACE FUNCTION financial_egress_preserve_lease() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF COALESCE((OLD.payload->>'egress_funded')::boolean,false) AND NOT COALESCE(
  (NEW.payload->>'egress_funded')::boolean
  AND (NEW.payload->>'egress_granted_bytes')::numeric>=(OLD.payload->>'egress_granted_bytes')::numeric
  AND (NEW.payload->>'egress_sequence')::numeric>=(OLD.payload->>'egress_sequence')::numeric,false) THEN
  RAISE EXCEPTION 'egress attempt receipt cannot discard funded bytes' USING ERRCODE='23514',CONSTRAINT='financial_egress_lease_monotonic';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER financial_egress_preserve_lease BEFORE UPDATE ON financial_compute_leases
 FOR EACH ROW EXECUTE FUNCTION financial_egress_preserve_lease();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'strict egress funding requires forward repair; rollback could erase byte authority'; END $$;
-- +goose StatementEnd

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
