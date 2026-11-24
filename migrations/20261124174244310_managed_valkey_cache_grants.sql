-- filename: 20261124174244310_managed_valkey_cache_grants.sql
-- migration-compat: expand
-- Binding a managed cache to an app grants that app the call
-- (ADR-20261124174500). `gregale redis bind` and Compose cache provisioning
-- write a row; `gregale redis unbind` deletes it. The service authorizer
-- admits a project caller to a cache outside its project only through a row.
-- Older daemons never read it.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS managed_valkey_cache_grants (
    cache_app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cache_app_id, app_id),
    CHECK (cache_app_id <> app_id)
);
CREATE INDEX IF NOT EXISTS managed_valkey_cache_grants_app_idx ON managed_valkey_cache_grants (app_id);

-- +goose Down
DROP TABLE IF EXISTS managed_valkey_cache_grants;
