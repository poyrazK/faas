-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113230000: new connection identities own one PKCE authorization.
ALTER TABLE tool_oauth_connections DROP CONSTRAINT tool_oauth_connections_state_check;
ALTER TABLE tool_oauth_connections ADD CONSTRAINT tool_oauth_connections_state_check
    CHECK(state IN ('authorization_pending','refresh_required','ready','refreshing','reconnect_required','revoked')) NOT VALID;
ALTER TABLE tool_oauth_connections VALIDATE CONSTRAINT tool_oauth_connections_state_check;

CREATE TABLE tool_oauth_authorizations (
    account_id uuid NOT NULL,
    connection_id uuid NOT NULL CHECK(connection_id <> '00000000-0000-0000-0000-000000000000'),
    state_sha256 text NOT NULL CHECK(state_sha256 ~ '^[0-9a-f]{64}$'),
    phase text NOT NULL CHECK(phase IN ('pending','exchanging','completed','failed')),
    expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)),
    flow_ciphertext bytea CHECK(flow_ciphertext IS NULL OR octet_length(flow_ciphertext) BETWEEN 1 AND 16384),
    PRIMARY KEY(account_id,connection_id),
    FOREIGN KEY(account_id,connection_id) REFERENCES tool_oauth_connections(account_id,id) ON DELETE CASCADE,
    CHECK((phase='pending')=(flow_ciphertext IS NOT NULL))
);

-- +goose Down
DROP TABLE tool_oauth_authorizations;
-- New guided records require the newer control plane; retain the expanded check.
