-- filename: 20261111140000000_complimentary_accounts.sql
-- migration-compat: expand
-- ADR-20261111140000: an operator can give an account a paid plan without a
-- subscription. One row per complimentary account. meterd neither bills nor
-- holds its usage; its spend cap is $0, so it stops at the plan's included
-- amounts; when expires_at passes, apid returns it to Free. apid is the only
-- writer (POST /v1/admin/ops/accounts/{id}/complimentary). A previous release
-- neither writes nor reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS complimentary_accounts (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    plan text NOT NULL CHECK (plan IN ('hobby', 'pro', 'scale')),
    reason text NOT NULL CHECK (reason ~ '^[a-z0-9_]{1,64}$'),
    granted_by uuid NOT NULL,
    granted_at timestamptz NOT NULL CHECK (isfinite(granted_at)),
    expires_at timestamptz CHECK (expires_at IS NULL OR (isfinite(expires_at) AND expires_at > granted_at))
);
CREATE INDEX IF NOT EXISTS complimentary_accounts_expires_at_idx
    ON complimentary_accounts (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE complimentary_accounts IS 'Accounts given a paid plan without a subscription; not billed, held to the plan allowance; written only by apid (ADR-20261111140000).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS complimentary_accounts;
-- +goose StatementEnd
