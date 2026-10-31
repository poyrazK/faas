-- filename: 20261030100950512_compute_node_cpu_pressure.sql
-- migration-compat: expand

-- ADR 20261030100950: the CPU pressure backstop. While a node's tenant tasks
-- wait for CPU and its CPUs are nearly all busy, the schedd that receives the
-- node's readings holds cpu_pressure_until ahead of now. Every schedd places
-- by the durable value: placement takes the node only when nothing else fits,
-- and admission counts its bookings in full. Nothing clears the column; it
-- expires.
--
-- The column is nullable with no default, so adding it writes no rows.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS cpu_pressure_until timestamptz;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE compute_nodes DROP COLUMN IF EXISTS cpu_pressure_until;

RESET lock_timeout;
