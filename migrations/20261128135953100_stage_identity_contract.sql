-- filename: 20261128135953100_stage_identity_contract.sql
-- migration-compat: contract
-- ADR-20261104120000 amendment 2026-11-22, release step 3: API consumers,
-- their keys and alert rules are unique per app and environment
-- (api_consumers_app_environment_external_ref_uniq,
-- consumer_keys_app_environment_prefix_idx,
-- consumer_keys_app_environment_name_idx,
-- alert_rules_app_environment_name_uniq). The app-wide indexes go, so a
-- stage can hold a consumer, key or rule under the name production uses
-- and a full clone can copy them. Every writer since rc.385 targets the
-- per-environment indexes; gateway key lookups filter by environment.
-- Dropped concurrently so consumer and rule writes never wait.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;
DROP INDEX CONCURRENTLY IF EXISTS api_consumers_app_external_ref_uniq;
DROP INDEX CONCURRENTLY IF EXISTS consumer_keys_app_prefix_idx;
DROP INDEX CONCURRENTLY IF EXISTS consumer_keys_unique_name;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_app_name_uniq;

-- +goose Down
-- A stage that reused a production name blocks these; delete or rename the
-- stage's row first.
SET lock_timeout = '5s';
SET statement_timeout = 0;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS api_consumers_app_external_ref_uniq ON api_consumers (app_id, external_ref);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS consumer_keys_app_prefix_idx ON consumer_keys (app_id, prefix);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS consumer_keys_unique_name ON consumer_keys (account_id, app_id, name);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_app_name_uniq ON alert_rules (app_id, name) WHERE (app_id IS NOT NULL);
