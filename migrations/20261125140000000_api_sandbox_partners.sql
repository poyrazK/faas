-- filename: 20261125140000000_api_sandbox_partners.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_partners (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 100),
    invite_hash bytea CHECK (invite_hash IS NULL OR octet_length(invite_hash) = 32),
    invited_at timestamptz,
    redeemed_at timestamptz,
    revoked_at timestamptz,
    consumer_key_id uuid,
    first_success_at timestamptz,
    CHECK (invite_hash IS NULL OR (invited_at IS NOT NULL AND redeemed_at IS NULL AND revoked_at IS NULL)),
    CHECK ((consumer_key_id IS NULL) = (redeemed_at IS NULL)),
    CHECK (first_success_at IS NULL OR (redeemed_at IS NOT NULL AND first_success_at >= redeemed_at))
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS api_sandbox_partners;
