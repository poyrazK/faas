-- filename: 20260908174300244_instances_app_deployment_idx_lowercase_states.sql

-- +goose Up
-- +goose StatementBegin
-- 00132 created instances_app_deployment_idx with UPPERCASE state literals:
--
--     ON instances (app_id, deployment_id)
--     WHERE state IN ('RUNNING', 'WAKING', 'COLD_BOOTING')
--
-- instances.state has been lowercase-constrained since 00001
-- (instances_state_check), and every other partial index on the table uses
-- lowercase. So the predicate matched zero rows: the index was empty, could
-- serve nothing, and still cost a maintenance write on every instance
-- INSERT and UPDATE. That is the exact Seq Scan on the per-deployment live
-- count that ADR-072 / issue #557 added it to remove.
--
-- Column order also changes, from (app_id, deployment_id) to
-- (deployment_id, app_id). The two readers are:
--
--   PgStore.CountLiveInstancesByDeployment
--     where deployment_id = $1 and state in (live)
--   PgStore.ConcurrencyForDeployment
--     where app_id = $1 and deployment_id = $2 and state in (live)
--
-- A btree leading on app_id cannot serve the first — its leading column is
-- unconstrained — so even a case-corrected (app_id, deployment_id) would
-- have left that query on a Seq Scan. Leading on deployment_id serves both:
-- the first as a plain scan, the second as a scan plus a cheap app_id
-- filter on an already-narrow result.
--
-- Not CONCURRENTLY: no migration in this repo uses it (ADR-049 discusses it
-- but ships nothing), goose runs statements in a transaction by default, and
-- the old index is empty so the drop is instant while the new index covers
-- only live rows, bounded by the RAM admission ceiling (§6.2-2).
--
-- Replay-safe: DROP ... IF EXISTS + CREATE ... IF NOT EXISTS.
DROP INDEX IF EXISTS instances_app_deployment_idx;

CREATE INDEX IF NOT EXISTS instances_app_deployment_idx
    ON instances (deployment_id, app_id)
    WHERE state IN ('waking', 'cold_booting', 'running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restore 00132's shape verbatim, including the uppercase predicate, so a
-- down-migration lands the schema this migration was applied against.
DROP INDEX IF EXISTS instances_app_deployment_idx;

CREATE INDEX IF NOT EXISTS instances_app_deployment_idx
    ON instances (app_id, deployment_id)
    WHERE state IN ('RUNNING', 'WAKING', 'COLD_BOOTING');
-- +goose StatementEnd
