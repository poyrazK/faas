-- filename: 20261029205635242_traffic_binding_claim_indexes.sql
-- ADR-20261029190349 decision 3: a scoped binding write reads only the claims
-- and reservations of its scope. These indexes serve its lookups by app, by
-- redirect app and by hostname suffix (reversed labels, prefix match).
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS custom_domains_app_idx ON custom_domains (app_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS custom_domains_redirect_app_idx ON custom_domains (app_id_redirect) WHERE app_id_redirect IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS custom_domains_reversed_idx ON custom_domains (reverse(lower(domain::text)) text_pattern_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS tenant_hostnames_reversed_idx ON tenant_hostnames (reverse(lower(hostname::text)) text_pattern_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS tenant_hostnames_reversed_idx;
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_reversed_idx;
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_redirect_app_idx;
DROP INDEX CONCURRENTLY IF EXISTS custom_domains_app_idx;
