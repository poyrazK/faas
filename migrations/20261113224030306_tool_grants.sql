-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113100001: only apid writes exact-version delegation intent.
CREATE TABLE IF NOT EXISTS tool_grants (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id uuid NOT NULL CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    version text NOT NULL,
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
    revoked_at timestamptz CHECK (revoked_at IS NULL OR isfinite(revoked_at)),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 8192),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,version) REFERENCES tool_publications(account_id,id) ON DELETE CASCADE,
    CHECK ((payload->>'id' = id::text AND payload->>'account_id' = account_id::text
        AND payload->'intent'->>'key_id' = key_id::text AND payload->'intent'->>'version' = version
        AND (payload->'intent'->>'expires_at')::timestamptz = expires_at
        AND COALESCE((payload->>'revoked_at')::timestamptz = revoked_at, payload->>'revoked_at' IS NULL AND revoked_at IS NULL)) IS TRUE)
);
CREATE INDEX IF NOT EXISTS tool_grants_key_idx ON tool_grants (account_id,key_id,version) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE tool_grants;
