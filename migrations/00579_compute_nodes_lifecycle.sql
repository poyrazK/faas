-- filename: 00579_compute_nodes_lifecycle.sql
-- Workstream B (issue #1184): replace `active bool` on compute_nodes with a
-- 4-state lifecycle enum (`active` / `draining` / `unavailable` / `recovering`).
-- The boolean `active` is preserved as a STORED GENERATED column so every
-- existing query, partial index, and pg_notify consumer continues to work
-- unchanged. The placement filter, heartbeat writer, and admin drain API
-- write `lifecycle` directly; downstream code keeps reading `active`.
--
-- Why an enum over a free-text column:
--  - the issue text lists 5 states (active / draining / unavailable / recovering)
--    but `recovering` is an implicit sub-phase of "node is back, instances still
--    healing" — modelling it as a state of its own (not a bool flag) lets the
--    recovery arbiter distinguish "freshly back" from "fully healthy" and emit
--    distinct metrics per phase.
--  - ENUM + CHECK on `last_recovery_outcome` (00582) gives runtime + migration-
--    time validation; no separate "is this string valid?" code path.
--
-- ADR-137 §"Lifecycle shape" records the deviation from issue #1184's text.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'compute_node_lifecycle') THEN
        CREATE TYPE compute_node_lifecycle AS ENUM (
            'active',
            'draining',
            'unavailable',
            'recovering'
        );
    END IF;
END$$;

ALTER TABLE compute_nodes
    ADD COLUMN IF NOT EXISTS lifecycle compute_node_lifecycle NOT NULL DEFAULT 'active';

-- Drop the boolean active and replace with a STORED GENERATED column that
-- exposes `active = (lifecycle IN ('active','recovering'))`. Existing
-- queries (`WHERE active = true`) keep working without rewrite.
ALTER TABLE compute_nodes DROP COLUMN IF EXISTS active;
ALTER TABLE compute_nodes
    ADD COLUMN active BOOLEAN
    GENERATED ALWAYS AS (lifecycle IN ('active','recovering')) STORED;

-- Replace the legacy partial index `compute_nodes_active_idx` (created in
-- 00024) with one on the generated column. Same predicate shape, same
-- performance characteristics.
DROP INDEX IF EXISTS compute_nodes_active_idx;
CREATE INDEX IF NOT EXISTS compute_nodes_lifecycle_idx
    ON compute_nodes (name) WHERE lifecycle IN ('active','recovering');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS compute_nodes_lifecycle_idx;
CREATE INDEX IF NOT EXISTS compute_nodes_active_idx
    ON compute_nodes (name) WHERE active = true;
ALTER TABLE compute_nodes DROP COLUMN IF EXISTS active;
ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE compute_nodes DROP COLUMN IF EXISTS lifecycle;
DROP TYPE IF EXISTS compute_node_lifecycle;
-- +goose StatementEnd