# ADR-122 — Safe releases (issue #976)

- **Status:** proposed
- **Date:** 2026-08-20
- **Decision:** A new orchestration layer in **meterd** is the
  authoritative engine for SAFE-RELEASES: progressive rollout, automated
  rollback, post-deploy health sampling, and per-deployment preview
  URLs. The `deployments` table is the unit of revision (no separate
  `revisions` table — every deployment is its own revision). Canary
  presets are gated on Pro+ plans (already gated on traffic-split,
  ADR-084). Audit + diff retention is 90 days. The per-deployment
  preview URL shape is `deploy-{N}.{slug}.{root_domain}` where the
  suffix is `*.gregale.dev` (the in-flight cert-wildcard target,
  NOT the legacy `*.apps.gregale.dev`).

  The work ships as two mega PRs:

  - **Mega PR #1 (foundation):** E.2 (deployment_audit table + 90-day
    backfill), C (per-deployment preview URLs), D (pkg/openapidiff +
    schema-drift gate).
  - **Mega PR #2 (headline):** A (canary presets), B (auto-rollback
    on first-N-5xx), F (meterd ticks goroutine that ties them
    together).

  Why split: Mega PR #1 is additive, schema-stable at the row level,
  and reviewable by the audit + surface owners. Mega PR #2 introduces
  the orchestrator and the failure-action paths that need a separate
  review surface for ops + meterd owners.

- **Why:** Today a deployment is fire-and-forget. The customer
  workflow when v3 of an app misbehaves in production is "find the
  broken commit, re-deploy v2 manually, hope the rollback doesn't
  share a snapshot, ignore the audit trail because `app.deployed`
  has actor='apid' regardless of who the human was." SOC 2 CC7.2 /
  GDPR Art. 32 require the platform to answer "who deployed v3 of
  app X at 14:32, and why did the traffic-split fail-over to v2 at
  14:36?" with structured data, not a Slack scrollback. PR #979-G
  shipped the manual `rollback_to_id` path (issue #976 / ADR-070
  precedent); the missing pieces are the automated detection +
  progressive rollout that make it actually safe to release.

  Concretely the gaps are:

  1. **Audit trail is per-emit, not per-deployment.** The
     `app.deployed` audit row is a one-shot event. There is no
     post-deploy audit chain ("at 14:36 traffic shifted 10%→v2,
     at 14:38 health probes turned red on v3, at 14:39 v3 was
     auto-promoted to 0%"). Closing this requires a structured
     `deployment_audit` row per deployment-event.
  2. **Preview is `app --pr N` only.** A deploying developer
     cannot share `deploy-{N}.{slug}.gregale.dev` with a teammate
     or a customer-success check without going through the GitHub
     PR preview seam (ADR-095). Closing this requires a
     deployment-scoped preview URL on the deployment itself.
  3. **Schema drift is silent.** The OpenAPI + state structs drift
     over time; `pkg/api/dto.go::DeploymentResponse` can change
     a field without anyone noticing until a customer integration
     breaks. Closing this requires an automated diff gate that
     classifies every schema change as BREAKING / NOISE / ADDITIVE.
  4. **Rollback is human-initiated only.** No first-N-5xx detector.
     No canary preset. The progressive-rollout story is
     "deploy to 100%, pray, rollback manually if you notice."
     Closing this requires the meterd orchestrator + canary
     preset surface.
  5. **Health signals are sampled, not orchestrated.** `schedd`
     samples wake latency; meterd samples GB-h. Neither has a
     post-deploy health gate that consumes both signals and acts.
     Closing this requires a single orchestrator goroutine in
     meterd with a 5s tick + bounded queue.

  All five gaps are interdependent — fixing #1 without #4-#5 is a
  write-only audit trail; fixing #4-#5 without #2 is an
  orchestrator that fires on traffic nobody can see. Mega PR #1
  fixes the read-side (audit + URL + drift gate), Mega PR #2
  fixes the write-side (orchestrator + canary + auto-rollback).

- **Consequences:**
  - **Orchestrator in meterd, not a new daemon.** New orchestrator
    goroutine lives at `pkg/meterd/release_orchestrator.go`
    (5s tick, `pkg/api/ReleaseOrchestratorTickIntervalSeconds=5`).
    meterd is the only daemon that already samples every per-app
    metric (GB-h, idle timeout, scale-up); adding a release
    orchestrator there reuses the meterd store handle +
    pg_notify subscribers without a new unix socket. schedd is
    excluded because schedd is the wake-fleet state owner and a
    release orchestrator would couple them at the wrong boundary
    (ADR-018). apid is excluded because the orchestrator must
    consume per-app metrics meterd samples, which apid does not.
  - **`deployments` is the unit of revision.** No
    `deployment_revisions` table. Every `INSERT INTO deployments`
    is a new revision; the `traffic_split_percent` column on
    `deployments` (migration 00160, ADR-084 PR-A) plus the
    `target_deployment_id` column on `apps` (PR #979-G, ADR-070
    precedent) plus the `status='superseded'` state (PR #979-G)
    already give us all the revision semantics we need. Adding
    a separate revisions table would duplicate `apps.id` FKs,
    double the migration surface, and require a new read-side
    join on every dashboard render.
  - **Canary presets gated on Pro+.** The `traffic_split_percent`
    column is already gated on Pro+ via `api.TrafficSplitAllowed()`
    + `Plan.TrafficSplitAllowed()` (ADR-084 §Decision 3). The new
    `canary_preset` enum (`{none, slow, balanced, aggressive}`)
    inherits the same gate. Free + Hobby plans see only `none`
    in the API response (the column is server-stamped with
    `none` on their rows, no client-side 403 dance).
  - **Audit + diff retention = 90 days.** The `deployment_audit`
    table has a partial index on
    `(deployment_id, received_at DESC)` plus a hard GC at
    `received_at < now() - 90 days`. The
    `pkg/openapidiff.Snapshot` cache evicts snapshots older
    than 90 days at the next load. 90 days is chosen because
    the spec §4.7 GB-h billing math uses 90-day customer
    retention as the floor; aligning the audit retention
    avoids two GC goroutines at different cadences.
  - **Preview URL shape `deploy-{N}.{slug}.{root_domain}`**
    where `root_domain='gregale.dev'` (the in-flight cert-wildcard
    target, NOT legacy `apps.gregale.dev`). The new helper
    `pkg/gateway/preview_parser.go::DeploymentScopeFromHost`
    is a sibling of the existing `ScopeFromHost` (PR-preview
    ADR-095) and is added to the same allowlist at
    `pkg/gateway/allowlist.go:24-58`. The hostname is
    server-stamped on the deployment row at INSERT time so the
    dashboard "copy URL" button has zero server-side cost on
    click.
  - **Two mega PRs, NOT seven atomic PRs.** The user picked the
    two-mega-PR plan (2026-08-20) over the seven-PR G-A..-G
    cluster because the cluster's review surface scales with
    reviewer patience, not atomic PR count, and the
    cross-PR slot precheck costs 5-7 minutes per atomic PR
    push on a saturated CI queue. The two-mega-PR plan
    trades per-PR review focus for two PRs that each ship
    under 10 reviewable commits.
  - **Schema changes land in Mega PR #1 only.** E.2 adds
    `deployment_audit` (1 migration + 1 NOT VALID FK). C
    adds zero new schema (preview URL is computed at
    request time from `deployment_id` + `app.slug`). D adds
    zero new schema (the diff package consumes the existing
    `pkg/apid/openapi.yaml`). Mega PR #2 is zero-schema.
  - **Migration slot coordination.** Mega PR #1 starts at
    `00363_deployment_audit.sql` (renumbered through 17 hops:
    `00320 → 00322 → 00324 → 00326 → 00327 → 00330 → 00332 →
    00334 → 00342 → 00346 → 00348 → 00350 → 00352 → 00354 →
    00356 → 00358 → 00360 → 00363` to clear open-PR slots #999 #990 #1004 #1000 +
    post-merge of PR #999's `00326_apps_public_auth_ip_allowlist.sql`
    on origin/main + the round-4 fence-collision on main's
    `00327_reserve_slot.sql` + `00328_reserve_slot.sql` fences +
    main's `00329_consumer_keys.sql` real migration + the round-5
    collision with open PR #1005's
    `00330_deployment_openapi_snapshots.sql` real migration +
    the round-6 contiguity gap created by PR #1005's
    reservation fences at slots `00331`/`00332`/`00333` +
    the round-9 rebase-vs-main bump past main's fence ceiling
    `00339` and main's `00341_repair_app_secrets_scope.sql`
    real migration + the round-10 rebase-vs-main bump past
    main's `00345_edge_rules_kind_cache.sql` real migration
    that landed via PR #1008 + #1013 + the round-11
    rebase-vs-main bump past main's
    `00346_deployments_annotation.sql` real migration that
    landed via PR #984 merging mid-round-10 + the round-12
    rebase-vs-main bump past PR #1017's full slot range
    `00347-00351` (5 real migrations shipped by PR #1017's
    ADR-123 alert-preset catalog while round-12 was in flight)
    + the round-13 rebase-vs-PR #1012 collision at slot 00352
    (PR #1012 re-fenced its 00347-00351 range and bumped its real
    migration to 00352 while round-13 was in flight) +
    the round-14 renumber to `00354`+`00355` to clear PR #1012's
    round-14 re-fence of its slot range + the round-15 bridge
    fences `00347-00353` to close the synthetic-merge contiguity
    gap + the round-16 renumber to `00356`+`00357` to clear
    PR #1024 (ADR-124 deployment queue controls) which opened
    2026-08-21T19:18Z and claimed slots `00353-00355` for its
    real migrations `deployments_cancelled` / `builds_cancelled`
    / `deployments_priority` + the round-17 renumber to
    `00358`+`00359` to clear the same-night collisions with
    PR #1012's renumber to 00356_deployments_stage_state_history_cap.sql,
    PR #1005's renumber to 00357_deployment_openapi_snapshots.sql,
    and PR #990's renumber to 00357_app_secret_value_hash.sql +
    the round-18 renumber to `00360`+`00361` plus 6 bridge fences
    `00354-00359` to clear PR #1023 (ADR-124 per-service
    wire-protocol selector) which initially claimed slot
    `00358` for `00358_apps_app_protocol.sql` + the round-19
    renumber to `00363`+`00364` after PR #1023 expanded its real
    claim to include `00360_apps_app_protocol.sql` post-rebase
    onto origin/main. The backfill sits at
    `00364_deployment_audit_backfill_90d.sql`. The
    cross-PR slot precheck
    (`scripts/ci/check_migration_slots.sh --base-ref=origin/main`)
    must be re-run at every push — the open-PR slot landscape
    moves every few hours under a saturated CI queue. Per
    ADR-041 fence carve-out, intermediate slots 00320-00325
    are claimed by other PRs' real migrations, so adding fences
    there would collide and was dropped in favour of renumbering.
  - **Cross-cluster references.** Mega PR #1's audit row
    references ADR-035 (auth audit log surface) for the
    `audit_log`-equivalent shape — the new `deployment_audit`
    table is the per-deployment counterpart of the global
    `audit_log`. Mega PR #2's canary preset references
    ADR-084 (traffic-split PR-C) for the picker signal and
    the largest-remainder redistribution.
  - **Spec §6.4 amendment 2.** Section §6.4 (deploy-time
    Problem) gains a paragraph noting that post-deploy
    failure actions are owned by `pkg/meterd/release_orchestrator.go`,
    not by apid handlers (this is the only spec change;
    Mega PR #2 carries the patch).

- **Rejected alternatives:**
  1. **New `release-orchestratord` daemon.** Adding a new
     daemon costs a unix socket + a systemd unit + a
     `pkg/daemonunit` entry + a cross-daemon wire contract.
     meterd already samples every per-app signal the
     orchestrator needs; reusing the meterd process avoids
     4 layers of new infrastructure for a goroutine.
  2. **Separate `deployment_revisions` table.** Doubles
     the migration surface, requires a new read-side join
     on every dashboard render, and the existing
     `traffic_split_percent` + `target_deployment_id` +
     `status='superseded'` columns already encode every
     revision semantic the orchestrator needs.
  3. **Seven atomic PRs (the original G-A..-G cluster).**
     Each atomic PR costs 5-7 minutes of CI queue time
     for the slot precheck; the cluster would land in
     3-5 calendar days of CI churn. The two-mega-PR plan
     lands in 1-2 calendar days with two reviews instead
     of seven, at the cost of slightly larger review
     artifacts.
  4. **Canary presets on all plans.** Free + Hobby customers
     do not have the traffic-split budget to actually
     exercise canary semantics; the canary preset would
     default to `none` anyway, which is the same as gating
     on Pro+. Gating cleanly is better UX (the API response
     shows `{none}` for Free/Hobby rather than offering
     a button that no-ops).
  5. **30-day or 365-day audit retention.** 30 days is too
     short to cover a SOC 2 audit cycle (90 days is the
     audit-default minimum). 365 days is wasteful for the
     95% of customers who ship daily (the audit table
     would balloon to ~50M rows per active tenant). 90 days
     is the floor from spec §4.7 GB-h billing math, so
     the GC cadence is one goroutine, not two.
  6. **Legacy `apps.gregale.dev` preview URL shape.** The
     in-flight cert-wildcard migration
     (`memory/hostname-wildcard-gregale-dev-not-apps.md`)
     targets `*.gregale.dev`. Using the legacy shape would
     force a second migration when the cert-wildcard
     ships; using the target shape now means zero work
     when the migration completes.
