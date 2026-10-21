-- filename: 20261020203204512_managed_postgres_live_logins.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks managed_postgres_databases briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261016151947: the login livequeryd decodes a live database with. When
-- live queries are enabled, apid issues it through the database's provider,
-- seals its connection string to livequeryd's age recipient and never reads it
-- back; disabling revokes it and drops its slot. livequeryd discovers the
-- databases it serves from these rows.
CREATE TABLE IF NOT EXISTS managed_postgres_live_logins (
    database_id          uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id           uuid NOT NULL,
    sealed_login         bytea NOT NULL CHECK (octet_length(sealed_login) BETWEEN 1 AND 8192),
    recipient            text NOT NULL CHECK (recipient ~ '^age1[0-9a-z]{58}$'),
    provider_identity_id text NOT NULL CHECK (provider_identity_id ~ '^[a-z0-9_]{1,63}$'),
    slot                 text NOT NULL CHECK (slot ~ '^[a-z0-9_]{1,63}$'),
    created_at           timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE managed_postgres_live_logins IS
    'ADR-20261016151947 replication login per live managed database, sealed to livequeryd; livequeryd discovers its databases here.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS managed_postgres_live_logins;
-- +goose StatementEnd
