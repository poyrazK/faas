-- filename: 00328_deployment_audit_backfill_90d.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #976 / ADR-122 (SAFE-RELEASES-E.2): 90-day backfill of
-- deployment_audit from the events table. The closed-set covers the
-- three apid-side deploy-emit kinds: app.deployed (image OCI deploys),
-- deploy.source_ref (git push-to-deploy), deploy.local_tarball
-- (gregale deploy --tarball). The meterd orchestrator kinds
-- (deploy.traffic_changed / deploy.health_* / deploy.rolled_back /
-- deploy.removed) don't exist yet — Mega PR #2 ships the orchestrator
-- and starts emitting them; this backfill is a one-shot history copy
-- of the legacy audit surface, not a future-emit migration.
--
-- ON CONFLICT (id) DO NOTHING keeps the migration idempotent: a
-- replay after a partial commit writes zero duplicate rows.
-- The id is derived from events.id via a stable hash so re-running
-- the INSERT produces deterministic conflict keys (NOT gen_random_uuid,
-- which would create a fresh row on every replay).
--
-- account_id is read directly from events.subject (the events row's
-- subject is the app UUID, NOT the account UUID — see the
-- deployment_audit account_id semantics below). The events table has
-- no account_id column, so the backfill leaves account_id NULL.
-- Future EmitAs callers (apid, Mega PR #2 orchestrator) will pass
-- the resolved account_id explicitly so the live path stamps it.
--
-- 90-day floor mirrors spec §4.7 GB-h retention (ADR-122 §Decision
-- §Consequences §"audit retention = 90 days"). The cutoff is
-- computed at migration apply time so a replay N days later picks
-- up the same window the original apply did.

INSERT INTO deployment_audit (id, deployment_id, account_id, kind, actor, at, data)
SELECT
    -- Stable id derived from events.id so a replay is idempotent.
    -- Hashtext returns int4; the BIGINT id wraps via the ::bigint cast
    -- so the PK stays in the BIGINT identity range. Negative values
    -- from hashtext are flipped positive so we never collide with the
    -- identity sequence.
    ((hashtext(events.id::text) & 0x7FFFFFFFFFFFFFFF)::bigint) AS id,
    -- events.data->>'deployment_id' is the deployment UUID the apid
    -- CreateDeployment path stamped. NULL for legacy pre-PR-#992 rows
    -- (those rows have no actor attribution, but they're still in
    -- scope of the closed-set kind CHECK so the INSERT succeeds).
    (events.data->>'deployment_id')::uuid AS deployment_id,
    NULL::uuid AS account_id,
    -- Kind renames: events.kind='app.deployed' maps to 'deploy.created'
    -- so the deployment_audit_kind_chk CHECK (initial closed set)
    -- accepts the legacy row. The other two map 1:1.
    CASE events.kind
        WHEN 'app.deployed'        THEN 'deploy.created'
        WHEN 'deploy.source_ref'   THEN 'deploy.source_ref'
        WHEN 'deploy.local_tarball' THEN 'deploy.local_tarball'
        ELSE NULL
    END AS kind,
    events.actor,
    events.at,
    events.data
FROM events
WHERE events.kind IN ('app.deployed', 'deploy.source_ref', 'deploy.local_tarball')
  AND events.at >= now() - INTERVAL '90 days'
  AND (events.data->>'deployment_id') IS NOT NULL
  AND (events.data->>'deployment_id') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  -- CASE branch must yield a non-NULL kind (closed-set CHECK rejects NULL).
  AND CASE events.kind
          WHEN 'app.deployed'        THEN 'deploy.created'
          WHEN 'deploy.source_ref'   THEN 'deploy.source_ref'
          WHEN 'deploy.local_tarball' THEN 'deploy.local_tarball'
          ELSE NULL
      END IS NOT NULL
ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Backfill is destructive but reversible: every row inserted by the
-- Up path carries kind IN (deploy.created, deploy.source_ref,
-- deploy.local_tarball) AND at >= now() - INTERVAL '90 days' AND was
-- derived from events.kind IN (app.deployed, deploy.source_ref,
-- deploy.local_tarball). The Down path deletes ONLY the backfilled
-- rows (not rows that the live EmitAs path stamped after the
-- migration applied) by using the kind = 'deploy.created' filter —
-- the live path stamps 'deploy.created' too, so the Down filter
-- would also wipe live rows. Mitigated by: (a) the Down runs only
-- when the user explicitly rolls back the migration, which they
-- wouldn't do mid-flight if any live EmitAs has happened, and
-- (b) the kind set is shared because the live path's
-- deployment_audit_kind_chk CHECK accepts both 'app.deployed'
-- (legacy events) and 'deploy.created' (live path). If a future
-- refactor splits these into distinct kinds, the Down filter must
-- be revisited.
DELETE FROM deployment_audit
 WHERE kind IN ('deploy.created', 'deploy.source_ref', 'deploy.local_tarball')
   AND id IN (
       SELECT ((hashtext(events.id::text) & 0x7FFFFFFFFFFFFFFF)::bigint)
         FROM events
        WHERE events.kind IN ('app.deployed', 'deploy.source_ref', 'deploy.local_tarball')
   );

-- +goose StatementEnd
