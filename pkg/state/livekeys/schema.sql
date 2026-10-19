-- Compile-time projection of the tables the live-query key queries read.
-- Runtime schema is owned by migration 20261017121336518 (and, for
-- managed_postgres_databases and managed_postgres_bindings, earlier managed
-- Postgres migrations). This file never executes against a database.
CREATE TABLE managed_postgres_databases (
 id uuid NOT NULL,
 account_id uuid NOT NULL,
 state text NOT NULL,
 backend_id text NOT NULL,
 backend_fingerprint text NOT NULL,
 deleted_at timestamptz
);
CREATE TABLE managed_postgres_bindings (
 account_id uuid NOT NULL,
 database_id uuid NOT NULL,
 app_id uuid NOT NULL,
 scope text NOT NULL,
 access text NOT NULL,
 provider_identity_id text,
 state text NOT NULL,
 created_at timestamptz NOT NULL,
 deleted_at timestamptz
);
CREATE TABLE managed_postgres_live_keys (
 kid text PRIMARY KEY,
 account_id uuid NOT NULL,
 database_id uuid NOT NULL,
 sealed_secret bytea NOT NULL,
 recipient text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 retires_at timestamptz
);
