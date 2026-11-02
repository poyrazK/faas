-- filename: 20261102103931945_status_tables_hot_updates.sql
-- migration-compat: expand
-- Three status tables are rewritten every few seconds, and each carries an
-- index on the timestamp every write changes. Postgres can only update a row
-- in place (HOT) when no indexed column changes, so every write also inserted
-- index entries and left dead tuples for autovacuum (prod, 2026-11-02, since
-- 2026-10-20):
--
--   operator_alert_rule_observations      18 updates/s, 9% HOT, 585 rows,
--     retention_idx 688 kB; read only by the prune (0.07/s)
--   app_scaling_policy_scheduler_status   2 updates/s, 0% HOT, 528 rows,
--     observed_idx 216 kB, never scanned, 42,285 autovacuums
--   app_health_collection_state           2 updates/s, 31% HOT, 523 rows,
--     due_idx 608 kB, never scanned, 29,505 autovacuums
--
-- Each table is a few hundred rows, so a sequential scan answers the prune
-- and any other read on these columns in well under a millisecond. Dropping
-- the indexes changes no result; the previous release runs unchanged.
--
-- Dropped concurrently (NO TRANSACTION), so the tables keep taking writes.
-- Re-runnable. Down rebuilds them concurrently.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

DROP INDEX CONCURRENTLY IF EXISTS operator_alert_rule_observations_retention_idx;
DROP INDEX CONCURRENTLY IF EXISTS app_scaling_policy_scheduler_status_observed_idx;
DROP INDEX CONCURRENTLY IF EXISTS app_health_collection_due_idx;

-- +goose Down
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS operator_alert_rule_observations_retention_idx
    ON operator_alert_rule_observations (evaluated_at, rule_key);
CREATE INDEX CONCURRENTLY IF NOT EXISTS app_scaling_policy_scheduler_status_observed_idx
    ON app_scaling_policy_scheduler_status (observed_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS app_health_collection_due_idx
    ON app_health_collection_state (next_check_at, app_id);
