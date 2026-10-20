-- filename: 20261020140000001_account_mfa_totp_last_step.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- RFC 6238 §5.2: a TOTP code is accepted once. apid records the 30-second
-- time step of the last code it accepted for a session and refuses any code
-- at or before it, so a code seen in transit cannot be replayed within its
-- validity window. NULL means no code has been accepted yet.
-- NOT VALID: every existing row is NULL, and new writes are still checked.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS mfa_totp_last_step bigint;
ALTER TABLE accounts ADD CONSTRAINT accounts_mfa_totp_last_step_chk
    CHECK (mfa_totp_last_step IS NULL OR mfa_totp_last_step >= 0) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_mfa_totp_last_step_chk;
ALTER TABLE accounts DROP COLUMN IF EXISTS mfa_totp_last_step;
-- +goose StatementEnd
