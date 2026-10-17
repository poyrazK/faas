-- filename: 20261014080534241_consumer_credits_and_spend_caps.sql

-- ADR-1306: prepaid credit grants per API consumer, applied to statements
-- when they are finalized, and monthly spend caps the gateway enforces.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_credit_grants (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id           uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id               uuid NOT NULL,
  consumer_id          uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
  currency             text NOT NULL,
  amount_millicents    bigint NOT NULL,
  remaining_millicents bigint NOT NULL,
  note                 text NOT NULL DEFAULT '',
  expires_at           timestamptz,
  voided_at            timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT api_consumer_credit_grants_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
  CONSTRAINT api_consumer_credit_grants_amount_chk
    CHECK (amount_millicents > 0 AND remaining_millicents >= 0 AND remaining_millicents <= amount_millicents),
  CONSTRAINT api_consumer_credit_grants_note_chk CHECK (char_length(note) <= 200),
  CONSTRAINT api_consumer_credit_grants_voided_chk CHECK (voided_at IS NULL OR remaining_millicents = 0),
  CONSTRAINT api_consumer_credit_grants_expiry_chk CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS api_consumer_credit_grants_consumer_idx
  ON api_consumer_credit_grants (consumer_id, currency, created_at);
COMMENT ON TABLE api_consumer_credit_grants IS 'Prepaid credit granted to an API consumer; finalized statements draw it down oldest-expiring first (ADR-1306).';

CREATE TABLE IF NOT EXISTS api_consumer_credit_applications (
  grant_id          uuid NOT NULL REFERENCES api_consumer_credit_grants(id) ON DELETE CASCADE,
  statement_id      uuid NOT NULL REFERENCES api_consumer_usage_statements(id) ON DELETE CASCADE,
  account_id        uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  amount_millicents bigint NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (grant_id, statement_id),
  CONSTRAINT api_consumer_credit_applications_amount_chk CHECK (amount_millicents > 0)
);
CREATE INDEX IF NOT EXISTS api_consumer_credit_applications_statement_idx
  ON api_consumer_credit_applications (statement_id);

ALTER TABLE api_consumer_usage_statements ADD COLUMN IF NOT EXISTS credit_applied_millicents bigint NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_usage_statements_credit_chk') THEN
    ALTER TABLE api_consumer_usage_statements ADD CONSTRAINT api_consumer_usage_statements_credit_chk
      CHECK (credit_applied_millicents >= 0 AND credit_applied_millicents <= amount_millicents);
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS api_consumer_spend_caps (
  consumer_id            uuid PRIMARY KEY REFERENCES api_consumers(id) ON DELETE CASCADE,
  account_id             uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id                 uuid NOT NULL,
  currency               text NOT NULL,
  monthly_cap_millicents bigint NOT NULL,
  capped                 boolean NOT NULL DEFAULT false,
  month_start            timestamptz,
  amount_due_millicents  bigint,
  checked_at             timestamptz,
  next_check_at          timestamptz NOT NULL DEFAULT now(),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT api_consumer_spend_caps_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
  CONSTRAINT api_consumer_spend_caps_cap_chk CHECK (monthly_cap_millicents >= 0),
  CONSTRAINT api_consumer_spend_caps_due_chk CHECK (amount_due_millicents IS NULL OR amount_due_millicents >= 0),
  CONSTRAINT api_consumer_spend_caps_capped_chk CHECK (NOT capped OR (month_start IS NOT NULL AND checked_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS api_consumer_spend_caps_next_check_idx ON api_consumer_spend_caps (next_check_at);
COMMENT ON TABLE api_consumer_spend_caps IS 'Monthly spend cap per API consumer; apid checks the month to date and the gateway denies capped consumers (ADR-1306).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_spend_caps;
ALTER TABLE api_consumer_usage_statements DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_credit_chk;
ALTER TABLE api_consumer_usage_statements DROP COLUMN IF EXISTS credit_applied_millicents;
DROP TABLE IF EXISTS api_consumer_credit_applications;
DROP TABLE IF EXISTS api_consumer_credit_grants;
-- +goose StatementEnd
