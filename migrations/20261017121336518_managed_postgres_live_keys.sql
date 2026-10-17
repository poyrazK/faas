-- filename: 20261017121336518_managed_postgres_live_keys.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1384: the live-query keys of a managed database. A database has live
-- queries while it has an active key (retires_at NULL); rotation keeps the
-- previous key until retires_at. apid seals each secret to livequeryd's age
-- recipient and never reads it back; the customer sees it once.
CREATE TABLE IF NOT EXISTS managed_postgres_live_keys (
    kid           text PRIMARY KEY CHECK (kid ~ '^[0-9a-f]{16}$'),
    account_id    uuid NOT NULL,
    database_id   uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    sealed_secret bytea NOT NULL CHECK (octet_length(sealed_secret) BETWEEN 1 AND 4096),
    recipient     text NOT NULL CHECK (recipient ~ '^age1[0-9a-z]{58}$'),
    created_at    timestamptz NOT NULL DEFAULT now(),
    retires_at    timestamptz CHECK (retires_at IS NULL OR retires_at > created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_live_keys_active_idx ON managed_postgres_live_keys (database_id) WHERE retires_at IS NULL;
CREATE INDEX IF NOT EXISTS managed_postgres_live_keys_database_idx ON managed_postgres_live_keys (database_id, created_at);
COMMENT ON TABLE managed_postgres_live_keys IS
    'ADR-1384 live-query secrets per managed database, sealed to livequeryd; at most one active and one retiring key.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS managed_postgres_live_keys;
-- +goose StatementEnd
