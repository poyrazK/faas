-- +goose Up
-- +goose StatementBegin

-- ADR-361 decision 6: an account-wide abuse hold. It is kept apart from
-- accounts.status so the billing lifecycle (past_due -> suspended ->
-- deleted_pending and its timers) never sees it, and releasing the hold
-- restores exactly the prior state. While set, the account behaves like a
-- suspended one: nothing boots, apps park and deploys are refused.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS abuse_hold_at timestamptz,
    ADD COLUMN IF NOT EXISTS abuse_hold_reason text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_abuse_hold_valid'
          AND conrelid = 'accounts'::regclass
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_abuse_hold_valid
            CHECK (
                (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
                AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'operator'))
            );
    END IF;
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE IF EXISTS accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
ALTER TABLE IF EXISTS accounts DROP COLUMN IF EXISTS abuse_hold_reason;
ALTER TABLE IF EXISTS accounts DROP COLUMN IF EXISTS abuse_hold_at;
-- +goose StatementEnd
