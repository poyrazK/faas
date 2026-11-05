-- filename: 20261105081450000_sandbox_ports.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock accounts and sandbox_sessions briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 §11 exposed ports. apid inserts and deletes the rows:
-- a session's exposed guest ports and their visibility. gatewayd-internal
-- reads them, with the session's placement, into a short-lived cache when it
-- routes a preview host; a request never reads them directly. A new table,
-- so older binaries keep working while this lands.
CREATE TABLE IF NOT EXISTS sandbox_ports (
    sandbox_id uuid NOT NULL REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    port       integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sandbox_id, port)
);
COMMENT ON TABLE sandbox_ports IS
    'ADR-20261027090013 §11 exposed sandbox ports; apid inserts and deletes rows; gatewayd-internal caches them to route preview hosts.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_ports;
-- +goose StatementEnd
