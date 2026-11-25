-- filename: 20261124203944100_unclaimed_accounts.sql
-- migration-compat: expand
-- ADR-20261124080000: claimable deploys. An agent deploys with no account;
-- apid creates a locked-down Free account for it and records it here until a
-- human claims it (claimed_at) or it expires and is scheduled for deletion.
-- The claim link carries a random token; only its SHA-256 is stored. The
-- table is new, so the previous release neither reads nor writes it.
-- Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS unclaimed_accounts (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    claim_token_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(claim_token_sha256) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    expired_at timestamptz,
    created_ip inet,
    user_agent text NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 512),
    CHECK (expires_at > created_at),
    CHECK (claimed_at IS NULL OR expired_at IS NULL)
);
COMMENT ON TABLE unclaimed_accounts IS 'Accounts created by an anonymous agent deploy, until a human claims them or they expire; written by apid (ADR-20261124080000).';

-- The expiry sweep and the live cap read open rows by expiry.
CREATE INDEX IF NOT EXISTS unclaimed_accounts_open_expires_idx
    ON unclaimed_accounts (expires_at)
    WHERE claimed_at IS NULL AND expired_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS unclaimed_accounts;
