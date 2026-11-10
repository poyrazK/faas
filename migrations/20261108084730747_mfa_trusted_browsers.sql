-- migration-compat: expand
-- filename: 20261108084730747_mfa_trusted_browsers.sql

-- +goose Up
-- Browsers an account chose to remember after passing its authenticator
-- check (ADR-20261108150000). The cookie carries the raw token; only its
-- keyed digest is stored. A sign-in from a remembered browser of the same
-- user-agent family skips the code until expires_at; revoking clears it.
-- Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS mfa_trusted_browsers (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    ua_family text NOT NULL,
    created_ip text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT mfa_trusted_browsers_pkey PRIMARY KEY (id),
    CONSTRAINT mfa_trusted_browsers_token_hash_key UNIQUE (token_hash),
    CONSTRAINT mfa_trusted_browsers_token_hash_check CHECK (octet_length(token_hash) IN (32, 33)),
    CONSTRAINT mfa_trusted_browsers_ua_family_check CHECK (char_length(ua_family) <= 64),
    CONSTRAINT mfa_trusted_browsers_created_ip_check CHECK (char_length(created_ip) <= 64),
    CONSTRAINT mfa_trusted_browsers_window_check CHECK (isfinite(created_at) AND expires_at > created_at),
    CONSTRAINT mfa_trusted_browsers_used_check CHECK (last_used_at IS NULL OR isfinite(last_used_at)),
    CONSTRAINT mfa_trusted_browsers_revoked_check CHECK (revoked_at IS NULL OR isfinite(revoked_at))
);
CREATE INDEX IF NOT EXISTS mfa_trusted_browsers_account_idx
    ON mfa_trusted_browsers (account_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mfa_trusted_browsers;
-- +goose StatementEnd
