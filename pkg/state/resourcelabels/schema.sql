-- Offline projection of the migration-owned tables whose rows carry labels
-- beside apps (ADR-20261031100025 step 5).
CREATE TABLE managed_postgres_databases (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object')
);

CREATE TABLE crons (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object')
);

CREATE TABLE jobs (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object')
);
