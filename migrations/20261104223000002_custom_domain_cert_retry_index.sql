-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- A canceled concurrent build may have left an invalid index with this name.
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_cert_retry_due_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS custom_domains_cert_retry_due_idx
    ON custom_domains ((coalesce(cert_retry_at, 'epoch'::timestamptz)), domain)
    WHERE verified_at IS NOT NULL AND domain LIKE '*.%';
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_cert_retry_due_idx;
RESET lock_timeout;
