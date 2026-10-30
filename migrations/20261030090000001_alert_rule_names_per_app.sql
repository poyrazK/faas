-- filename: 20261030090000001_alert_rule_names_per_app.sql
-- migration-compat: contract
-- ADR-20261029203000: an alert rule's name is unique within its app (and an
-- account-wide rule's within its account), not across the account, so two
-- apps can each have a "high-errors" rule and a deleted app's rules no longer
-- hold names for the other apps. The new indexes are built without blocking
-- writes before the account-wide one goes; replacing an interrupted build
-- also removes the invalid index PostgreSQL leaves.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_app_name_uniq;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_app_name_uniq
    ON alert_rules (app_id, name) WHERE app_id IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_account_wide_name_uniq;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_account_wide_name_uniq
    ON alert_rules (account_id, name) WHERE app_id IS NULL;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_account_name_uniq;
RESET lock_timeout;

-- +goose Down
-- Restoring the account-wide index fails if two apps now share a rule name;
-- rename one of them first.
SET lock_timeout = '5s';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_account_name_uniq
    ON alert_rules (account_id, name);
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_account_wide_name_uniq;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_app_name_uniq;
RESET lock_timeout;
