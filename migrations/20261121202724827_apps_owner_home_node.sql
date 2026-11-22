-- filename: 20261121202724827_apps_owner_home_node.sql
-- migration-compat: expand

-- +goose Up
-- adr: 20261121203000 — the node an app was displaced from by a drain or a
-- dead-node ownership recovery. Set by ReassignOrphanedAppOwner, cleared by
-- every other ownership move and when the app returns home. The column and
-- its foreign key lock apps and compute_nodes briefly.
SET LOCAL lock_timeout = '5s';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS owner_home_node_id uuid
  REFERENCES compute_nodes(id) ON DELETE SET NULL;

-- +goose Down
-- Expand-only rollback: older binaries ignore the column.
SELECT 1;
