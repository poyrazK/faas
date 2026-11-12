-- filename: 20261110153935524_sandbox_sessions.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock accounts and platform_tenants briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 sandbox sessions: a live microVM that an agent harness
-- running elsewhere uses as its computer. apid inserts the queued intent,
-- closes a queued session and requests the close of a live one; agentd
-- (ADR-20261027090014) claims sessions under a lease and owns every other
-- transition. Terminal states are immutable and carry closed_at. A new
-- table, so older binaries keep working while this lands.
CREATE TABLE IF NOT EXISTS sandbox_sessions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tenant_id        uuid,
    kind             text NOT NULL DEFAULT 'ephemeral' CHECK (kind IN ('ephemeral', 'persistent')),
    preset           text NOT NULL CHECK (char_length(preset) BETWEEN 1 AND 64),
    ram_mb           integer NOT NULL CHECK (ram_mb BETWEEN 512 AND 4096 AND ram_mb % 512 = 0),
    network_mode     text NOT NULL DEFAULT 'registries' CHECK (network_mode IN ('none', 'registries', 'web')),
    state            text NOT NULL DEFAULT 'queued' CHECK (state IN (
                         'queued', 'starting', 'ready', 'frozen', 'closed', 'expired', 'failed')),
    idle_timeout_sec integer NOT NULL CHECK (idle_timeout_sec BETWEEN 1 AND 86400),
    expires_at       timestamptz NOT NULL,
    labels           jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(labels) = 'object'),
    outcome_code     text CHECK (outcome_code IS NULL OR outcome_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    outcome_message  text CHECK (outcome_message IS NULL OR char_length(outcome_message) <= 2048),
    lease_owner      text CHECK (lease_owner IS NULL OR char_length(lease_owner) BETWEEN 1 AND 128),
    lease_token      text CHECK (lease_token IS NULL OR char_length(lease_token) BETWEEN 1 AND 128),
    lease_expires_at timestamptz,
    node_id          text CHECK (node_id IS NULL OR char_length(node_id) BETWEEN 1 AND 128),
    instance_id      text CHECK (instance_id IS NULL OR char_length(instance_id) BETWEEN 1 AND 128),
    close_requested_at timestamptz,
    last_activity_at timestamptz NOT NULL DEFAULT now(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    closed_at        timestamptz,
    CONSTRAINT sandbox_sessions_lease_check CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT sandbox_sessions_closed_check CHECK ((state IN ('closed', 'expired', 'failed')) = (closed_at IS NOT NULL)),
    CONSTRAINT sandbox_sessions_tenant_fkey FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants (account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS sandbox_sessions_account_created_idx ON sandbox_sessions (account_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS sandbox_sessions_queue_idx ON sandbox_sessions (created_at, id) WHERE state = 'queued';
CREATE INDEX IF NOT EXISTS sandbox_sessions_live_idx ON sandbox_sessions (account_id, state)
    WHERE state IN ('queued', 'starting', 'ready', 'frozen');
COMMENT ON TABLE sandbox_sessions IS
    'ADR-20261027090013 sandbox sessions; apid inserts queued intents, closes queued ones and requests the close of live ones; agentd (ADR-20261027090014) owns every other transition.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_sessions;
-- +goose StatementEnd
