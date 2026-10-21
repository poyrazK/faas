-- filename: 20261020190822931_instances_recovery_node_live_idx.sql
-- migration-compat: expand
-- Index the recovery runner's per-node read (InstanceListByNodeForRecovery).
-- Its state set includes snapshotting, migrating and frozen, which
-- instances_live_node_id_idx does not cover, so every one-second sweep of an
-- unavailable node sequentially scanned instances on every scheduler: on
-- production-us, 6.4 scans and 184,000 rows a second. The same index was
-- built by hand on production-us on 2026-10-20; replacing it here also
-- removes an invalid index an interrupted build would leave behind.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS instances_recovery_node_live_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS instances_recovery_node_live_idx ON instances (node_id, started_at)
    WHERE state IN ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm', 'frozen');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS instances_recovery_node_live_idx;
RESET lock_timeout;
