-- filename: 20261017143756964_platform_tenant_fees_trials_proration.sql

-- ADR-1322: platform tenant cards and plans can charge a monthly base fee and
-- minimum spend, tenant statements carry those fees, and a tenant can have a
-- free trial. ADR-1325: app and tenant cards can prorate a month's fees for
-- a consumer or tenant created during it. Re-runnable.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE platform_tenant_rate_cards ADD COLUMN IF NOT EXISTS base_fee_millicents_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_rate_cards ADD COLUMN IF NOT EXISTS minimum_millicents_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_rate_cards ADD COLUMN IF NOT EXISTS prorate_monthly_fees boolean NOT NULL DEFAULT false;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_rate_cards_monthly_fees_chk' AND conrelid = 'platform_tenant_rate_cards'::regclass) THEN
    ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_monthly_fees_chk
      CHECK (base_fee_millicents_per_month >= 0 AND minimum_millicents_per_month >= 0);
  END IF;
END $$;
ALTER TABLE platform_tenant_plans ADD COLUMN IF NOT EXISTS base_fee_millicents_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_plans ADD COLUMN IF NOT EXISTS minimum_millicents_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_plans ADD COLUMN IF NOT EXISTS prorate_monthly_fees boolean NOT NULL DEFAULT false;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_plans_monthly_fees_chk' AND conrelid = 'platform_tenant_plans'::regclass) THEN
    ALTER TABLE platform_tenant_plans ADD CONSTRAINT platform_tenant_plans_monthly_fees_chk
      CHECK (base_fee_millicents_per_month >= 0 AND minimum_millicents_per_month >= 0);
  END IF;
END $$;
ALTER TABLE api_consumer_rate_cards ADD COLUMN IF NOT EXISTS prorate_monthly_fees boolean NOT NULL DEFAULT false;
ALTER TABLE platform_tenant_statements ADD COLUMN IF NOT EXISTS fees jsonb NOT NULL DEFAULT '[]'::jsonb;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_statements_fees_chk' AND conrelid = 'platform_tenant_statements'::regclass) THEN
    ALTER TABLE platform_tenant_statements ADD CONSTRAINT platform_tenant_statements_fees_chk
      CHECK (jsonb_typeof(fees) = 'array' AND jsonb_array_length(fees) <= 64);
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS platform_tenant_trials (
    platform_tenant_id uuid PRIMARY KEY REFERENCES platform_tenants(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT platform_tenant_trials_window_chk CHECK (ends_at > starts_at),
    CONSTRAINT platform_tenant_trials_minute_chk
      CHECK (starts_at = date_trunc('minute', starts_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
         AND ends_at = date_trunc('minute', ends_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);
CREATE INDEX IF NOT EXISTS platform_tenant_trials_account_idx ON platform_tenant_trials (account_id);
COMMENT ON TABLE platform_tenant_trials IS
  'A platform tenant''s free trial: its usage in [starts_at, ends_at) is priced at zero (ADR-1322).';
-- Finalized-statement webhooks carry the fees amount_millicents includes.
CREATE OR REPLACE FUNCTION enqueue_platform_tenant_statement_finalized_webhooks() RETURNS trigger
    LANGUAGE plpgsql
    AS $fn$
DECLARE
  event_payload jsonb;
  tenant_external_ref text;
BEGIN
  IF OLD.status = 'finalized' OR NEW.status <> 'finalized' THEN
    RETURN NEW;
  END IF;

  SELECT external_ref INTO tenant_external_ref
    FROM platform_tenants
   WHERE account_id = NEW.account_id AND id = NEW.platform_tenant_id;

  event_payload := jsonb_build_object(
    'platform_tenant_id', NEW.platform_tenant_id,
    'external_ref', tenant_external_ref,
    'statement_id', NEW.id,
    'revision', NEW.revision,
    'status', NEW.status,
    'period_start', NEW.period_start,
    'period_end', NEW.period_end,
    'currency', NEW.currency,
    'billable_units', NEW.billable_units,
    'unpriced_units', NEW.unpriced_units,
    'amount_millicents', NEW.amount_millicents,
    'priced', NEW.unpriced_units = 0 AND NEW.currency <> '',
    'lines', NEW.lines,
    'fees', NEW.fees,
    'as_of', NEW.as_of,
    'finalized_at', NEW.finalized_at
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, NEW.account_id, 'platform_tenant.statement.finalized', event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = NEW.platform_tenant_id
     AND h.account_id = NEW.account_id
     AND h.enabled
     AND 'platform_tenant.statement.finalized' = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$fn$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_platform_tenant_statement_finalized_webhooks() RETURNS trigger
    LANGUAGE plpgsql
    AS $fn$
DECLARE
  event_payload jsonb;
  tenant_external_ref text;
BEGIN
  IF OLD.status = 'finalized' OR NEW.status <> 'finalized' THEN
    RETURN NEW;
  END IF;

  SELECT external_ref INTO tenant_external_ref
    FROM platform_tenants
   WHERE account_id = NEW.account_id AND id = NEW.platform_tenant_id;

  event_payload := jsonb_build_object(
    'platform_tenant_id', NEW.platform_tenant_id,
    'external_ref', tenant_external_ref,
    'statement_id', NEW.id,
    'revision', NEW.revision,
    'status', NEW.status,
    'period_start', NEW.period_start,
    'period_end', NEW.period_end,
    'currency', NEW.currency,
    'billable_units', NEW.billable_units,
    'unpriced_units', NEW.unpriced_units,
    'amount_millicents', NEW.amount_millicents,
    'priced', NEW.unpriced_units = 0 AND NEW.currency <> '',
    'lines', NEW.lines,
    'as_of', NEW.as_of,
    'finalized_at', NEW.finalized_at
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, NEW.account_id, 'platform_tenant.statement.finalized', event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = NEW.platform_tenant_id
     AND h.account_id = NEW.account_id
     AND h.enabled
     AND 'platform_tenant.statement.finalized' = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$fn$;
DROP TABLE IF EXISTS platform_tenant_trials;
ALTER TABLE platform_tenant_statements DROP CONSTRAINT IF EXISTS platform_tenant_statements_fees_chk;
ALTER TABLE platform_tenant_statements DROP COLUMN IF EXISTS fees;
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS prorate_monthly_fees;
ALTER TABLE platform_tenant_plans DROP CONSTRAINT IF EXISTS platform_tenant_plans_monthly_fees_chk;
ALTER TABLE platform_tenant_plans DROP COLUMN IF EXISTS prorate_monthly_fees;
ALTER TABLE platform_tenant_plans DROP COLUMN IF EXISTS minimum_millicents_per_month;
ALTER TABLE platform_tenant_plans DROP COLUMN IF EXISTS base_fee_millicents_per_month;
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_monthly_fees_chk;
ALTER TABLE platform_tenant_rate_cards DROP COLUMN IF EXISTS prorate_monthly_fees;
ALTER TABLE platform_tenant_rate_cards DROP COLUMN IF EXISTS minimum_millicents_per_month;
ALTER TABLE platform_tenant_rate_cards DROP COLUMN IF EXISTS base_fee_millicents_per_month;
-- +goose StatementEnd
