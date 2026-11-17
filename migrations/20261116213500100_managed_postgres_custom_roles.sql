-- filename: 20261116213500100_managed_postgres_custom_roles.sql
-- migration-compat: expand
-- Custom Postgres group roles (ADR-20261116213000). A customer declares
-- NOLOGIN roles on one managed database and grants them to that database's
-- own Gregale logins; apid applies them through the provider's admin path and
-- the binding reconciler retries unsettled rows. A deleting row stays until
-- the role has been dropped from the database. New table only; older apid
-- never reads it.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_custom_roles (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    name text NOT NULL CHECK (name ~ '^[a-z_][a-z0-9_]{0,62}$'),
    bypass_rls boolean NOT NULL DEFAULT false,
    granted_to text[] NOT NULL DEFAULT '{}'
        CHECK (granted_to <@ ARRAY['migration', 'read_only', 'read_write']::text[]),
    member_of text[] NOT NULL DEFAULT '{}' CHECK (cardinality(member_of) <= 16),
    state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 128),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (database_id, name)
);

CREATE INDEX IF NOT EXISTS managed_postgres_custom_roles_due_idx
    ON managed_postgres_custom_roles (retry_at) WHERE state <> 'ready';

-- +goose Down
DROP TABLE IF EXISTS managed_postgres_custom_roles;
