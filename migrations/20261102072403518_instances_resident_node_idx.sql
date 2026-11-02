-- migration-compat: expand
-- ADR 20261102072403: schedd's instance stats poller reads the resident
-- instances on its own node every 200 ms. It used to read every live
-- instance in the fleet and keep its node's in Go; at a thousand instances a
-- node that is tens of thousands of rows a second from the control-plane
-- database. instances_live_node_id_idx leaves out frozen, snapshotting and
-- migrating rows, and frozen VMs are most of a dense node, so this partial
-- index covers every state that holds memory on a node. The previous release
-- never uses it.
--
-- The index is built concurrently (NO TRANSACTION), so instances keeps taking
-- writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS instances_resident_node_idx
    ON instances (node_id)
    WHERE state IN ('waking', 'cold_booting', 'running', 'draining', 'snapshotting', 'migrating', 'warm', 'frozen');

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('instances_resident_node_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'instances_resident_node_idx is invalid: DROP INDEX CONCURRENTLY instances_resident_node_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS instances_resident_node_idx;
RESET lock_timeout;
