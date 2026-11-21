-- filename: 20261119182736100_managed_valkey_caches.sql
-- migration-compat: expand
-- Managed Redis-compatible caches on Valkey (ADR-20261116192126). A cache is
-- an internal app running the pinned Valkey image; this row marks the app as
-- managed and holds the cache's settings. Older daemons never read it.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS managed_valkey_caches (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    memory_mb integer NOT NULL CHECK (memory_mb BETWEEN 32 AND 4096 AND memory_mb % 32 = 0),
    eviction_policy text NOT NULL CHECK (eviction_policy IN (
        'allkeys-lru','allkeys-lfu','allkeys-random',
        'volatile-lru','volatile-lfu','volatile-random','volatile-ttl','noeviction')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS managed_valkey_caches_account_idx ON managed_valkey_caches (account_id);

-- +goose Down
DROP TABLE IF EXISTS managed_valkey_caches;
