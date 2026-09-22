-- filename: 20260922183947369_credit_ledger_provider_scope.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-220: stop all legacy credit-consumption/refund writers before cutover.
-- They neither stamp nor filter provider and cannot share the new index.
-- Follow the refund path's invoice -> ledger order if a writer is draining.
DO $$
DECLARE
    provider_attnum smallint;
BEGIN
    LOCK TABLE invoices, credit_ledger IN ACCESS EXCLUSIVE MODE;

    SELECT attnum INTO provider_attnum
    FROM pg_attribute
    WHERE attrelid = 'credit_ledger'::regclass
      AND attname = 'provider' AND NOT attisdropped;
    IF provider_attnum IS NOT NULL THEN
        -- A lost goose row can replay a fully applied migration. Do not run
        -- the historical backfill again: later invoice evidence is not proof
        -- of an old ledger row's provider. Partial DDL needs an operator audit.
        IF NOT EXISTS (
            SELECT 1 FROM pg_index AS idx
            JOIN pg_class AS name ON name.oid = idx.indexrelid
            WHERE idx.indrelid = 'credit_ledger'::regclass
              AND name.relname = 'credit_ledger_invoice_credit_idx'
              AND idx.indisunique AND idx.indisvalid
              AND idx.indnkeyatts = 3 AND idx.indpred IS NOT NULL
              AND idx.indkey[0] = provider_attnum
              AND idx.indkey[1] = (
                  SELECT attnum FROM pg_attribute
                  WHERE attrelid = 'credit_ledger'::regclass
                    AND attname = 'provider_invoice_id' AND NOT attisdropped
              )
              AND idx.indkey[2] = (
                  SELECT attnum FROM pg_attribute
                  WHERE attrelid = 'credit_ledger'::regclass
                    AND attname = 'credit_id' AND NOT attisdropped
              )
        ) OR NOT EXISTS (
            SELECT 1 FROM pg_constraint
            WHERE conrelid = 'credit_ledger'::regclass
              AND conname = 'credit_ledger_provider_check'
              AND contype = 'c' AND convalidated
        ) THEN
            RAISE EXCEPTION 'credit ledger provider column exists without the complete provider-scoped schema; audit before recording the migration';
        END IF;
        RETURN;
    END IF;

    ALTER TABLE credit_ledger ADD COLUMN provider text NOT NULL DEFAULT ''
        CONSTRAINT credit_ledger_provider_check
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
END $$;
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
