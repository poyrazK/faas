-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113220000: execution authorization is separate from monitoring.
CREATE TABLE IF NOT EXISTS tool_oauth_connections (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id uuid NOT NULL CHECK(id <> '00000000-0000-0000-0000-000000000000'),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16384),
    state text NOT NULL CHECK(state IN ('refresh_required','ready','refreshing','reconnect_required','revoked')),
    generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
    credentials_ciphertext bytea CHECK(credentials_ciphertext IS NULL OR octet_length(credentials_ciphertext) BETWEEN 1 AND 131072),
    expires_at timestamptz CHECK(expires_at IS NULL OR isfinite(expires_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    revoked_at timestamptz CHECK(revoked_at IS NULL OR isfinite(revoked_at)),
    PRIMARY KEY(account_id,id),
    CHECK((state='refreshing')=(lease_id IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK(state<>'ready' OR credentials_ciphertext IS NOT NULL AND expires_at IS NOT NULL),
    CHECK((state='revoked')=(revoked_at IS NOT NULL)),
    CHECK((payload->>'id'=id::text AND payload->>'account_id'=account_id::text AND payload->>'app_id'=app_id::text) IS TRUE)
);
CREATE INDEX IF NOT EXISTS tool_oauth_connections_app_idx ON tool_oauth_connections(account_id,app_id,id);

-- +goose Down
DROP TABLE tool_oauth_connections;
