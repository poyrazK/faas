-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1748. Delivery must remain bounded under changing per-hour allowances.
-- This feature is unpublished. Never silently reprice existing funded books.
DO $$
DECLARE retained_currency text;
BEGIN
 IF to_regclass('financial_funding_currency') IS NOT NULL THEN
  EXECUTE 'SELECT currency FROM financial_funding_currency WHERE singleton' INTO retained_currency;
 END IF;
 IF retained_currency IS DISTINCT FROM 'USD' AND EXISTS(SELECT 1 FROM financial_compute_account_books WHERE COALESCE((payload->'egress'->>'initialized')::boolean,false)) THEN
  RAISE EXCEPTION 'existing egress candidate books require explicit forward repair before pinned delivery terms';
 END IF;
END $$;

CREATE OR REPLACE FUNCTION financial_egress_price_ceiling() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b jsonb;
BEGIN
 IF NEW.meter!='egress' THEN RETURN NEW; END IF;
 PERFORM 1 FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 SELECT payload->'egress' INTO b FROM financial_compute_account_books
 WHERE account_id=NEW.account_id AND period_start=NEW.period_start;
 IF COALESCE((b->>'initialized')::boolean,false) AND
  (NEW.price->>'currency' IS DISTINCT FROM 'EUR' OR NEW.price->>'unit' IS DISTINCT FROM 'interface_bytes'
   OR NOT COALESCE((NEW.price->>'included_quantity')::numeric>=(b->>'allowance_granted')::numeric,false)
   OR NOT COALESCE((NEW.price->>'unit_quantity')::numeric>0 AND (NEW.price->>'millicents_per_unit')::numeric>=0
    AND (NEW.price->>'millicents_per_unit')::numeric*(b->>'rate_quantity')::numeric
     <=(b->>'rate_millicents')::numeric*(NEW.price->>'unit_quantity')::numeric,false)) THEN
  RAISE EXCEPTION 'egress price or allowance exceeds funded period terms' USING ERRCODE='23514',CONSTRAINT='financial_egress_price_ceiling';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION financial_egress_preserve_book() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_egress jsonb := OLD.payload->'egress'; new_egress jsonb := NEW.payload->'egress';
BEGIN
 IF COALESCE((old_egress->>'initialized')::boolean,false) AND NOT COALESCE(
  (new_egress->>'initialized')::boolean
  AND (new_egress->>'quantity_committed')::numeric>=(old_egress->>'quantity_committed')::numeric
  AND (new_egress->>'quantity_committed')::numeric+(new_egress->>'quantity_reserved')::numeric
   >=(old_egress->>'quantity_committed')::numeric+(old_egress->>'quantity_reserved')::numeric
  AND new_egress->'allowance_granted'=old_egress->'allowance_granted'
  AND (new_egress->>'committed')::numeric>=(old_egress->>'committed')::numeric
  AND new_egress->'rate_millicents'=old_egress->'rate_millicents'
  AND new_egress->'rate_quantity'=old_egress->'rate_quantity',false) THEN
  RAISE EXCEPTION 'egress book authority cannot be discarded or reduced' USING ERRCODE='23514',CONSTRAINT='financial_egress_book_monotonic';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'strict egress delivery terms require forward repair'; END $$;
-- +goose StatementEnd

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
