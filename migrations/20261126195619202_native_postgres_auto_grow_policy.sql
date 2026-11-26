-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS native_postgres_auto_grow (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    max_bytes bigint NOT NULL CHECK (max_bytes >= 0),
    updated_at timestamptz NOT NULL,
    checked_at timestamptz,
    reason text NOT NULL DEFAULT 'waiting_for_pressure'
);
