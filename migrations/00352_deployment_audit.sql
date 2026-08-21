-- filename: 00352_deployment_audit.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #976 / ADR-122 (SAFE-RELEASES-E.2): per-deployment audit table
-- that survives deployment GC and gives the orchestrator a queryable
-- post-deploy timeline ("at 14:36 traffic shifted 10%->v2, at 14:38
-- health probes turned red on v3, at 14:39 v3 was auto-promoted to
-- 0%"). The events table is per-emit, append-only, and indexed for
-- subject lookups; this table is per-deployment, append-only, and
-- indexed for deployment_id lookups. The two tables coexist
-- (deployment_audit is the structured counterpart of the events
-- stream for the deploy lifecycle).
--
-- Shape: BIGINT IDENTITY PK (matches events table convention so the
-- dashboards team doesn't have to learn a new id type). deployment_id
-- has NO FK to deployments(id) on purpose — a deployment row may be
-- deleted by 90-day retention (deployment_audit outlives it, like
-- audit_log outlives accounts per issue #755 / PR-5). account_id is
-- nullable + has NO FK to accounts(id) for the same reason (a deleted
-- account does not cascade a deployment_audit row — the SOC 2 / GDPR
-- audit trail is preserved across account deletion).
--
-- kind is a closed set checked by the deployment_audit_kind_chk CHECK
-- constraint. Initial closed set matches the meterd orchestrator's
-- emit surface (Mega PR #2 adds the orchestrator goroutine that
-- writes these rows):
--
--   deploy.created             — apid CreateDeployment path
--   deploy.source_ref          — apid source-ref path
--   deploy.local_tarball       — apid tarball path
--   deploy.traffic_changed     — meterd orchestrator (canary step)
--   deploy.health_probe_failed — meterd orchestrator (first-N-5xx gate)
--   deploy.health_recovered    — meterd orchestrator (recovery)
--   deploy.rolled_back         — meterd orchestrator (auto-rollback)
--   deploy.removed             — meterd orchestrator (90-day GC)
--
-- data is the verbatim jsonb payload the orchestrator wrote at emit
-- time, mirroring the audit_log shape (migrations/00163). For
-- deploy.traffic_changed it carries {from_percent, to_percent};
-- for deploy.health_probe_failed it carries {probe, value, threshold};
-- for deploy.rolled_back it carries {trigger_kind, trigger_value,
-- restored_to_deployment_id}.
--
-- Index: (deployment_id, at DESC) is the dashboard-default sort order
-- ("show me the timeline for this deployment"). The (at) index
-- supports the 90-day GC sweep at meterd release_orchestrator startup
-- (Mega PR #2's GC goroutine reuses the same cron-tick seam as the
-- existing audit_log GC at pkg/state/pgstore.go).

CREATE TABLE IF NOT EXISTS deployment_audit (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deployment_id   UUID        NOT NULL,
    account_id      UUID,
    kind            TEXT        NOT NULL,
    actor           TEXT        NOT NULL,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    data            JSONB
);

-- closed-set kind check — initial set covers the Mega PR #1 emit
-- surface (apid CreateDeployment path, source_ref path, tarball
-- path). Mega PR #2 widens this set in a follow-up migration when
-- the meterd orchestrator lands.
ALTER TABLE deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit
    ADD CONSTRAINT deployment_audit_kind_chk
        CHECK (kind IN (
            'deploy.created',
            'deploy.source_ref',
            'deploy.local_tarball',
            'deploy.traffic_changed',
            'deploy.health_probe_failed',
            'deploy.health_recovered',
            'deploy.rolled_back',
            'deploy.removed'
        ));

-- dashboard-default sort: deployment timeline, newest first.
CREATE INDEX IF NOT EXISTS deployment_audit_deployment_idx
    ON deployment_audit (deployment_id, at DESC);

-- GC sweep index: meterd orchestrator runs DELETE FROM deployment_audit
-- WHERE at < now() - INTERVAL '90 days' every 6h. PG requires partial-
-- index predicates to use IMMUTABLE functions only, and now() is
-- VOLATILE — so the WHERE clause cannot carry the time-bound
-- predicate (the index would be rejected at CREATE INDEX time).
-- The meterd sweep filters on at < now() - INTERVAL '90 days' at
-- query time; the index below keeps the at column ordered so the
-- DELETE range scan stays sub-millisecond at the 90-day tail.
CREATE INDEX IF NOT EXISTS deployment_audit_at_gc_idx
    ON deployment_audit (at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS deployment_audit_at_gc_idx;
DROP INDEX IF EXISTS deployment_audit_deployment_idx;
ALTER TABLE IF EXISTS deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
DROP TABLE IF EXISTS deployment_audit;

-- +goose StatementEnd
