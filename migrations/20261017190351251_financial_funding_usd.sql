-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1430 / ADR-1642: new funding is USD. Historical EUR budget revisions,
-- prices and invoices remain unchanged. Pre-release books lack a currency tag;
-- never reinterpret their retained money as USD during an upgrade.
CREATE TABLE IF NOT EXISTS financial_funding_currency (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 currency text NOT NULL CHECK (currency='USD')
);
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM financial_funding_currency WHERE singleton AND currency='USD') AND (
 EXISTS (SELECT 1 FROM financial_compute_books)
 OR EXISTS (SELECT 1 FROM financial_compute_account_books)
 OR EXISTS (SELECT 1 FROM financial_compute_job_books)
 OR EXISTS (SELECT 1 FROM financial_compute_leases WHERE strict AND NOT COALESCE((payload->>'denied')::boolean,false))
 OR EXISTS (SELECT 1 FROM financial_budget_policies
   WHERE deleted_at IS NULL AND spec->>'enabled'='true' AND spec->>'currency'!='USD')) THEN
  RAISE EXCEPTION 'pre-release funding or enabled non-USD budgets require explicit forward repair before USD funding';
 END IF;
 INSERT INTO financial_funding_currency(singleton,currency) VALUES(true,'USD') ON CONFLICT DO NOTHING;
END $$;

CREATE OR REPLACE FUNCTION financial_egress_price_ceiling() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b jsonb;
BEGIN
 IF NEW.meter!='egress' THEN RETURN NEW; END IF;
 PERFORM 1 FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 SELECT payload->'egress' INTO b FROM financial_compute_account_books
 WHERE account_id=NEW.account_id AND period_start=NEW.period_start;
 IF COALESCE((b->>'initialized')::boolean,false) AND
  (NEW.price->>'currency' IS DISTINCT FROM 'USD' OR NEW.price->>'unit' IS DISTINCT FROM 'interface_bytes'
   OR NOT COALESCE((NEW.price->>'included_quantity')::numeric>=(b->>'allowance_granted')::numeric,false)
   OR NOT COALESCE((NEW.price->>'unit_quantity')::numeric>0 AND (NEW.price->>'millicents_per_unit')::numeric>=0
    AND (NEW.price->>'millicents_per_unit')::numeric*(b->>'rate_quantity')::numeric
     <=(b->>'rate_millicents')::numeric*(NEW.price->>'unit_quantity')::numeric,false)) THEN
  RAISE EXCEPTION 'egress price or allowance exceeds funded period terms' USING ERRCODE='23514',CONSTRAINT='financial_egress_price_ceiling';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'USD financial funding requires coordinated forward repair'; END $$;
-- +goose StatementEnd

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
