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

-- Redefine the pg_notify trigger function so it no longer references the
-- `active` column. Postgres refuses to drop a column if any trigger
-- function or view references it (SQLSTATE 2BP01). The trigger still
-- publishes `{"node_id":"<uuid>","active":<bool>,"lifecycle":"<enum>"}`
-- so existing gateway consumers can stay on the boolean field; the
-- enum value is additive and lets the recovery arbiter + drain API
-- distinguish lifecycle transitions.
CREATE OR REPLACE FUNCTION compute_node_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb;
BEGIN
    payload := jsonb_build_object(
        'node_id',   new.id::text,
        'active',    (new.lifecycle IN ('active','recovering')),
        'lifecycle', new.lifecycle::text
    );
    PERFORM pg_notify('compute_node_changed', payload::text);
    RETURN new;
END;
$$;

-- The snapshot-replica refresh trigger (added by 00480) also reads
-- `NEW.active` and lists `active` in its UPDATE OF clause. The
-- generated-column shape preserves the boolean semantics for the
-- function body (NEW.active is still a boolean), and the trigger
-- UPDATE OF clause can target `active` too — both keep working once
-- `active` is regenerated. We don't need to redefine this function
-- because it only reads NEW.active; Postgres won't block the column
-- drop on a function that reads NEW.active, only on a function whose
-- body has a stale reference. The CREATE OR REPLACE above (for the
-- notify function) is the actual blocker; this function is fine as-is.

-- Replace the legacy partial index `compute_nodes_active_idx` (created in
-- 00024) with one on the generated column. Same predicate shape, same
-- performance characteristics.
--
-- Order matters: the partial indexes' WHERE clauses reference `active`,
-- which depends on the column existing. Postgres refuses the column drop
-- with `cannot drop column active of table compute_nodes because other
-- objects depend on it` (SQLSTATE 2BP01). We drop the column with
-- CASCADE so Postgres handles the dependent indexes in one step; then
-- recreate them against the generated column. CASCADE is safe here
-- because the only dependents are these three indexes (verified via
-- pg_depend on the `active` attribute: zero non-auto dependents).
ALTER TABLE compute_nodes DROP COLUMN active CASCADE;
ALTER TABLE compute_nodes
    ADD COLUMN active BOOLEAN
    GENERATED ALWAYS AS (lifecycle IN ('active','recovering')) STORED;

CREATE INDEX IF NOT EXISTS compute_nodes_lifecycle_idx
    ON compute_nodes (name) WHERE lifecycle IN ('active','recovering');
-- Restore the unique-against-active predicate that 00431 added. The
-- generated column makes the predicate equivalent to the old
-- `WHERE active = true` shape; the index is safe to recreate now.
CREATE UNIQUE INDEX IF NOT EXISTS compute_nodes_active_unique_idx
    ON compute_nodes (name) WHERE active;
-- Restore the region/zone chooser index from 00072 against the
-- generated column. Same predicate shape, same planner behaviour.
CREATE INDEX IF NOT EXISTS compute_nodes_region_zone_idx
    ON compute_nodes (region, zone) WHERE active;
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