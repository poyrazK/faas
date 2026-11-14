-- migration-compat: expand
-- Bounded recent-verification catch-up must not scan the domain inventory on
-- each edge during a flood. Build online so domain verification keeps writing.
-- +goose NO TRANSACTION

-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_recent_verified_idx;
CREATE INDEX CONCURRENTLY custom_domains_recent_verified_idx
    ON custom_domains (verified_at DESC) WHERE verified_at IS NOT NULL;
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_recent_verified_idx;
RESET lock_timeout;
