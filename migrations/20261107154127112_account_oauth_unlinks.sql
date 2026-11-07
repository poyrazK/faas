-- migration-compat: expand
-- filename: 20261107154127112_account_oauth_unlinks.sql

-- +goose Up
-- Providers a customer removed from their account (ADR-20261107103000).
-- Signing in with that provider no longer re-links it by matching email;
-- only a browser already signed in to the account can link it again.
-- Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS account_oauth_unlinks (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider text NOT NULL,
    unlinked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_oauth_unlinks_pkey PRIMARY KEY (account_id, provider),
    CONSTRAINT account_oauth_unlinks_provider_check CHECK (provider IN ('google', 'github')),
    CONSTRAINT account_oauth_unlinks_unlinked_at_check CHECK (isfinite(unlinked_at))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_oauth_unlinks;
-- +goose StatementEnd
