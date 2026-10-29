-- filename: 20261026095955220_edge_rules_account_enabled_idx.sql
-- migration-compat: expand
-- ADR-091 amendment (2026-10-26): gatewayd loads a host's edge rules for the
-- account that owns the host only. No index leads with account_id, so without
-- this one each load would still scan every account's enabled rules.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS edge_rules_account_enabled_idx ON edge_rules (account_id)
    WHERE enabled;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS edge_rules_account_enabled_idx;
RESET lock_timeout;
