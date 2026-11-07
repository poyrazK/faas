-- filename: 20261107102846648_account_email_changes.sql

-- +goose Up
-- +goose StatementBegin
-- Pending account email changes (ADR-20261107103000). A signed-in customer
-- asks to move the account to new_email; the confirmation link mailed there
-- carries the token whose keyed digest is token_hash. Confirming switches
-- accounts.email and marks it verified. One pending change per account.
SET LOCAL lock_timeout = '5s';
CREATE TABLE account_email_changes (
    token_hash bytea PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    old_email text NOT NULL,
    new_email text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_email_changes_token_hash_check CHECK (length(token_hash) IN (32, 33)),
    CONSTRAINT account_email_changes_new_email_check CHECK (
        new_email = lower(new_email) AND length(new_email) BETWEEN 3 AND 254
        AND position(chr(10) IN new_email) = 0 AND position(chr(13) IN new_email) = 0),
    CONSTRAINT account_email_changes_old_email_check CHECK (length(old_email) <= 254),
    CONSTRAINT account_email_changes_window_check CHECK (expires_at > created_at),
    CONSTRAINT account_email_changes_consumed_check CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX account_email_changes_one_pending_idx
    ON account_email_changes (account_id) WHERE consumed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_email_changes;
-- +goose StatementEnd
