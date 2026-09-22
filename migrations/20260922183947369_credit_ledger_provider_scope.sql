-- filename: 20260922183947369_credit_ledger_provider_scope.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-213: stop all legacy credit-consumption/refund writers before cutover.
-- They neither stamp nor filter provider and cannot share the new index.
-- Follow the refund path's invoice -> ledger order if a writer is draining.
LOCK TABLE invoices, credit_ledger IN ACCESS EXCLUSIVE MODE;

ALTER TABLE credit_ledger ADD COLUMN provider text NOT NULL DEFAULT ''
    CHECK (provider IN ('', 'stripe', 'paddle', 'polar'));

-- Issuance rows have no invoice. Missing or ambiguous invoice evidence stays
-- unqualified: runtime rejects those keys until an operator resolves them.
-- Never guess a provider or change credit balances during this backfill.
WITH resolved AS (
    SELECT account_id, provider_invoice_id, min(provider) AS provider
    FROM invoices
    GROUP BY account_id, provider_invoice_id
    HAVING count(DISTINCT provider) = 1
)
UPDATE credit_ledger AS ledger
SET provider = resolved.provider
FROM resolved
WHERE ledger.account_id = resolved.account_id
  AND ledger.provider_invoice_id = resolved.provider_invoice_id;

DROP INDEX credit_ledger_invoice_credit_idx;
CREATE UNIQUE INDEX credit_ledger_invoice_credit_idx
    ON credit_ledger (provider, provider_invoice_id, credit_id)
    WHERE provider_invoice_id IS NOT NULL AND delta_cents < 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A downgrade is unsafe once providers reuse a credit/invoice pair. Let the
-- old uniqueness check abort the transaction; never erase audit history to
-- force a downgrade. Stop writers before attempting rollback, too.
DROP INDEX credit_ledger_invoice_credit_idx;
CREATE UNIQUE INDEX credit_ledger_invoice_credit_idx
    ON credit_ledger (provider_invoice_id, credit_id)
    WHERE provider_invoice_id IS NOT NULL AND delta_cents < 0;
ALTER TABLE credit_ledger DROP COLUMN provider;
-- +goose StatementEnd
