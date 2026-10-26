-- migration-compat: expand
-- filename: 20261026144852000_managed_postgres_database_endpoints.sql
--
-- ADR-20261026150000: the host names and ports a managed database's
-- credentials connect to, recorded by apid's credential sink whenever it seals
-- one (they are not secret). gatewayd-internal reads them to open the database
-- port to bound apps on every plan. A new table; the previous release never
-- reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS managed_postgres_database_endpoints (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    host text NOT NULL CHECK (length(host) BETWEEN 1 AND 253 AND host ~ '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$'),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (database_id, host, port)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS managed_postgres_database_endpoints;
-- +goose StatementEnd
