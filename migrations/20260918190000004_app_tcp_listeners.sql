-- Durable app-owned raw TCP listener identities (ADR-183).
-- The public port belongs to the app/listener, never to an instance or node.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_tcp_listeners (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id        UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    listener_name TEXT NOT NULL,
    guest_port    INTEGER NOT NULL,
    public_port   INTEGER NOT NULL,
    protocol      TEXT NOT NULL DEFAULT 'tcp',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT app_tcp_listeners_name_len_chk
        CHECK (char_length(listener_name) BETWEEN 1 AND 31),
    CONSTRAINT app_tcp_listeners_name_shape_chk
        CHECK (listener_name ~ '^[a-z0-9][a-z0-9-]{0,30}$'),
    CONSTRAINT app_tcp_listeners_guest_port_chk
        CHECK (guest_port BETWEEN 1 AND 65535),
    CONSTRAINT app_tcp_listeners_public_port_chk
        CHECK (public_port BETWEEN 40000 AND 49999),
    CONSTRAINT app_tcp_listeners_protocol_chk
        CHECK (protocol = 'tcp')
);

CREATE UNIQUE INDEX IF NOT EXISTS app_tcp_listeners_app_name_uniq
    ON app_tcp_listeners (app_id, listener_name);
CREATE UNIQUE INDEX IF NOT EXISTS app_tcp_listeners_public_port_uniq
    ON app_tcp_listeners (public_port);
CREATE INDEX IF NOT EXISTS app_tcp_listeners_app_created_idx
    ON app_tcp_listeners (app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS app_tcp_listeners_enabled_port_idx
    ON app_tcp_listeners (public_port)
    WHERE enabled;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_tcp_listeners;
-- +goose StatementEnd
