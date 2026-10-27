-- filename: 20261019103751562_platform_tenant_credits_and_spend_caps.sql

-- ADR-20261019100331: prepaid credit grants per platform tenant, drawn down
-- when its statements are finalized, and monthly spend caps that tenant
-- admission enforces, as ADR-1306 gives app consumers. The finalized
-- statement webhook carries the credit applied and the amount due.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS platform_tenant_credit_grants (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id           uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  platform_tenant_id   uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
  currency             text NOT NULL,
  amount_millicents    bigint NOT NULL,
  remaining_millicents bigint NOT NULL,
  note                 text NOT NULL DEFAULT '',
  expires_at           timestamptz,
  voided_at            timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT platform_tenant_credit_grants_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
  CONSTRAINT platform_tenant_credit_grants_amount_chk
    CHECK (amount_millicents > 0 AND remaining_millicents >= 0 AND remaining_millicents <= amount_millicents),
  CONSTRAINT platform_tenant_credit_grants_note_chk CHECK (char_length(note) <= 200),
  CONSTRAINT platform_tenant_credit_grants_voided_chk CHECK (voided_at IS NULL OR remaining_millicents = 0),
  CONSTRAINT platform_tenant_credit_grants_expiry_chk CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS platform_tenant_credit_grants_tenant_idx
  ON platform_tenant_credit_grants (platform_tenant_id, currency, created_at);
COMMENT ON TABLE platform_tenant_credit_grants IS 'Prepaid credit granted to a platform tenant; finalized tenant statements draw it down oldest-expiring first (ADR-20261019100331).';

CREATE TABLE IF NOT EXISTS platform_tenant_credit_applications (
  grant_id          uuid NOT NULL REFERENCES platform_tenant_credit_grants(id) ON DELETE CASCADE,
  statement_id      uuid NOT NULL REFERENCES platform_tenant_statements(id) ON DELETE CASCADE,
  account_id        uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  amount_millicents bigint NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (grant_id, statement_id),
  CONSTRAINT platform_tenant_credit_applications_amount_chk CHECK (amount_millicents > 0)
);
CREATE INDEX IF NOT EXISTS platform_tenant_credit_applications_statement_idx
  ON platform_tenant_credit_applications (statement_id);

ALTER TABLE platform_tenant_statements ADD COLUMN IF NOT EXISTS credit_applied_millicents bigint NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_statements_credit_chk'
                   AND conrelid = 'platform_tenant_statements'::regclass) THEN
    ALTER TABLE platform_tenant_statements ADD CONSTRAINT platform_tenant_statements_credit_chk
      CHECK (credit_applied_millicents >= 0 AND credit_applied_millicents <= greatest(amount_millicents, 0));
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS platform_tenant_spend_caps (
  platform_tenant_id     uuid PRIMARY KEY REFERENCES platform_tenants(id) ON DELETE CASCADE,
  account_id             uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  currency               text NOT NULL,
  monthly_cap_millicents bigint NOT NULL,
  capped                 boolean NOT NULL DEFAULT false,
  month_start            timestamptz,
  amount_due_millicents  bigint,
  checked_at             timestamptz,
  next_check_at          timestamptz NOT NULL DEFAULT now(),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT platform_tenant_spend_caps_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
  CONSTRAINT platform_tenant_spend_caps_cap_chk CHECK (monthly_cap_millicents >= 0),
  CONSTRAINT platform_tenant_spend_caps_due_chk CHECK (amount_due_millicents IS NULL OR amount_due_millicents >= 0),
  CONSTRAINT platform_tenant_spend_caps_capped_chk CHECK (NOT capped OR (month_start IS NOT NULL AND checked_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS platform_tenant_spend_caps_next_check_idx ON platform_tenant_spend_caps (next_check_at);
COMMENT ON TABLE platform_tenant_spend_caps IS 'Monthly spend cap per platform tenant; apid checks the month to date and tenant admission denies capped tenants (ADR-20261019100331).';

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
    'credit_applied_millicents', NEW.credit_applied_millicents,
    'amount_due_millicents', NEW.amount_millicents - NEW.credit_applied_millicents,
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
DROP TABLE IF EXISTS platform_tenant_spend_caps;
ALTER TABLE platform_tenant_statements DROP CONSTRAINT IF EXISTS platform_tenant_statements_credit_chk;
ALTER TABLE platform_tenant_statements DROP COLUMN IF EXISTS credit_applied_millicents;
DROP TABLE IF EXISTS platform_tenant_credit_applications;
DROP TABLE IF EXISTS platform_tenant_credit_grants;
-- +goose StatementEnd
