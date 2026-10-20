-- filename: 20261019195512417_statement_invoice_export_claims.sql

-- ADR-20261019180000: one attempt at a time exports a statement. An attempt
-- claims the export row until claimed_until; an export the provider refused
-- before creating anything records rejected_at, and the next attempt may
-- retarget it as a new attempt.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports
  ADD COLUMN IF NOT EXISTS attempt integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS rejected_at timestamptz,
  ADD COLUMN IF NOT EXISTS claim_token uuid,
  ADD COLUMN IF NOT EXISTS claimed_until timestamptz;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_attempt_chk'
                   AND conrelid = 'usage_statement_invoice_exports'::regclass) THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_attempt_chk
      CHECK (attempt >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_claim_chk'
                   AND conrelid = 'usage_statement_invoice_exports'::regclass) THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_claim_chk
      CHECK ((claim_token IS NULL) = (claimed_until IS NULL));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_statement_invoice_exports_rejected_chk'
                   AND conrelid = 'usage_statement_invoice_exports'::regclass) THEN
    ALTER TABLE usage_statement_invoice_exports ADD CONSTRAINT usage_statement_invoice_exports_rejected_chk
      CHECK (rejected_at IS NULL OR provider_invoice_id IS NULL);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE usage_statement_invoice_exports
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_rejected_chk,
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_claim_chk,
  DROP CONSTRAINT IF EXISTS usage_statement_invoice_exports_attempt_chk,
  DROP COLUMN IF EXISTS claimed_until,
  DROP COLUMN IF EXISTS claim_token,
  DROP COLUMN IF EXISTS rejected_at,
  DROP COLUMN IF EXISTS attempt;
-- +goose StatementEnd
