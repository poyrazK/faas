-- filename: 20261013144321184_account_totp_failures.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1152: apid runs on several control nodes, so the per-account TOTP
-- failure window (10 failures per 15 minutes) must be shared, or each
-- replica would grant its own budget of guesses. One row per failed code;
-- rows older than the window are pruned on the next failure and all rows
-- for an account are removed on a correct code.
CREATE TABLE IF NOT EXISTS account_totp_failures (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    failed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS account_totp_failures_account_idx
    ON account_totp_failures (account_id, failed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_totp_failures;
-- +goose StatementEnd
