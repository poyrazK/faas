-- filename: 00336_apps_static_egress_ip.sql
-- +goose Up
-- +goose StatementBegin

-- 00336_apps_static_egress_ip.sql — ADR-119 (static outbound IP per app).
--
-- Adds two nullable columns to `apps` plus a partial unique index:
--
--   static_egress_ip          INET NULL
--   static_egress_ip_set_at   TIMESTAMPTZ NULL
--
-- The customer BYOIPs an IPv4 from their own range; the host bridge
-- aliases it and a per-host postrouting MASQUERADE sibling rewrites
-- matching tenant source traffic to the customer's IP (see
-- pkg/netns/policy.go::Render in the host renderer). Single-node v1
-- — multi-host placement pin / IPv6 / platform-owned pool / Paddle
-- add-on billing are explicit follow-up ADRs.
--
-- Why a column-on-apps (not a child table):
--   * Per-app quota is 1 in v1 (Limits.StaticEgressIPsPerApp=1 for
--     Scale; 0 otherwise). A child table is the right shape if/when
--     we raise the per-app quota to N (currently 1), but is not
--     needed for v1. Mirrors ADR-031 (`apps.egress_allowlist`) and
--     the `accounts.egress_allowlist_extra` integer (ADR-082).
--     Bumping later is a one-time migration.
--   * IPv4 only in v1 — enforced by `apps_static_egress_ip_family_check`
--     below (family()=4). The v6 mirror is deferred to follow-up.
--   * Defended at the DB layer by `apps_static_egress_ip_key` partial
--     unique index — two apps on the same account cannot pin the
--     same IP (alias-IP collision on br-tenants). SQLSTATE 23505
--     surfaces in the apid handler as ErrPlanStaticEgressIPQuota.
--
-- Wire path:
--   * apid PATCH /v1/apps/{slug}/static-egress-ip → set
--   * apid DELETE /v1/apps/{slug}/static-egress-ip → null
--   * pg_notify('app_changed') — sched/egress_drift subscriber
--     fires UpdateStaticEgressIP gRPC to patch live instances.
--
-- Replay-safety (PR #377 / ADR-041):
--   * ALTER TABLE … ADD COLUMN IF NOT EXISTS (idempotent)
--   * The CHECK constraint is added via DO-block guard — PG rejects
--     `ADD CONSTRAINT IF NOT EXISTS` (SQLSTATE 42710 on second pass);
--     same idiom as 00302_deployments_stage_state.sql:60-75 and
--     00286_data_upstreams_deployment_scope.sql:51-62.
--   * CREATE UNIQUE INDEX IF NOT EXISTS (idempotent).
--   * The harness at migrations/replay_safety_test.go
--     (TestNewMigrationsAreReplaySafe) pins the second pass as a
--     no-op.

-- 1) The two columns. NULL default; no table rewrite.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS static_egress_ip inet NULL;

ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS static_egress_ip_set_at timestamptz NULL;

-- 2) Family=4 CHECK constraint, DO-block guarded. IPv6 is deferred.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'apps_static_egress_ip_family_check'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_static_egress_ip_family_check
            CHECK (static_egress_ip IS NULL OR family(static_egress_ip) = 4);
    END IF;
END$$;

-- 3) Partial unique index — two apps on the same account cannot pin
-- the same IP (alias-IP collision on br-tenants). The index is
-- partial on `WHERE static_egress_ip IS NOT NULL` so the NULL rows
-- do not participate (NULLs are not considered equal for unique
-- purposes in PG; partial index makes the contract explicit).
CREATE UNIQUE INDEX IF NOT EXISTS apps_static_egress_ip_key
    ON apps (static_egress_ip)
    WHERE static_egress_ip IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse the migration. The unique index must be dropped BEFORE
-- the column (otherwise PG raises SQLSTATE 42P16 "cannot drop
-- index ... constraint ... depends on it" if the column had a
-- CHECK linking it; here the index is partial so the order is
-- less load-bearing, but we keep the safe order anyway).
DROP INDEX IF EXISTS apps_static_egress_ip_key;

-- The CHECK constraint is dropped with IF EXISTS so a down on a
-- drifted DB does not trip 42710.
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_static_egress_ip_family_check;

ALTER TABLE apps DROP COLUMN IF EXISTS static_egress_ip_set_at;
ALTER TABLE apps DROP COLUMN IF EXISTS static_egress_ip;

-- +goose StatementEnd