-- filename: 20261122072552357_meter_network_checkpoint_node.sql
-- migration-compat: expand
--
-- ADR-20261122090000: a live migration moves an instance to a node whose vmmd
-- counts its network bytes from zero, while the source node's final reading
-- (served by schedd as a departed row) was dropped whenever the destination
-- already reported. The checkpoint now records which node its counter came
-- from, so meterd applies the source's final reading once, starts the
-- destination's counter from zero, and ignores the source afterwards. Empty
-- means a checkpoint written before this column (no node known).

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE meter_network_checkpoints ADD COLUMN IF NOT EXISTS node_id text NOT NULL DEFAULT '';

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE meter_network_checkpoints DROP COLUMN IF EXISTS node_id;
