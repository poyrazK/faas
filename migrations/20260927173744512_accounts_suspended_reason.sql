-- +goose Up
-- +goose StatementBegin

-- Why an account is suspended, where the answer decides how the suspension
-- ends. 'free_quota' is the Free plan's monthly hard stop: it lifts once the
-- account is back under its included usage (a new month, or an upgrade).
-- NULL covers every other suspension — dunning, identified by past_due_at,
-- and operator action — which only a payment or an operator lifts. Before
-- this column the quota stop was indistinguishable from an operator
-- suspension, so nothing ever lifted it.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS suspended_reason text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_suspended_reason_check'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_suspended_reason_check
            CHECK (suspended_reason IS NULL
                   OR (suspended_reason = 'free_quota' AND status = 'suspended'));
    END IF;
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_suspended_reason_check;
ALTER TABLE accounts DROP COLUMN IF EXISTS suspended_reason;
-- +goose StatementEnd
