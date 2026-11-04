-- filename: 20261025110817920_snapshot_cpu_platform_slots.sql
-- migration-compat: contract
-- Per-platform snapshot slots (ADR-20261024225612 amendment 1). A deployment
-- keeps one live snapshot per tier per CPU platform: the capturing node's
-- template when it used one, otherwise its host CPU signature
-- (state.SnapshotCPUPlatform computes the same expression). Nodes that
-- cannot share snapshots each keep a restorable capture instead of
-- replacing each other's on every park.
--
-- The new index is built concurrently before the old (deployment_id, tier)
-- one is dropped, so uniqueness holds throughout. Contract: dropping the old
-- unique index lets a deployment hold one live row per platform, which a
-- binary from before the ADR would not choose between. Rows written before
-- the ADR all share the empty platform and still conflict with each other.

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS snapshots_deployment_tier_platform_key
    ON snapshots (deployment_id, tier, (CASE WHEN cpu_template <> '' THEN 'template:' || cpu_template ELSE cpu_model END))
    WHERE stale = false;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS snapshots_deployment_tier_key;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Fails while a deployment holds live rows of one tier on two platforms;
-- mark all but one stale first.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS snapshots_deployment_tier_key
    ON snapshots (deployment_id, tier) WHERE stale = false;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS snapshots_deployment_tier_platform_key;
-- +goose StatementEnd
