# ADR-064 · Tier A4: cross-node app rebalance (post-drain owner recovery)

- **Status:** proposed
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-08-01
- **Decision:** When `compute_nodes.active` flips to `false` (operator
  drain in `docs/runbooks/gate-a.md`, or future heartbeat-staleness
  watchdog), every parked/stopped app owned by the drained node is
  atomically re-assigned to one of the remaining active peer schedds.
  Gated by admission headroom, paced by a per-app cooldown
  (`api.RebalanceCooldownSeconds`, default 60s), and capped per drain
  event (`api.RebalanceMaxPerTickPerNode`, default 50). One-way
  (dead → alive); live-instance migration is out of scope.

## Context

ADR-062 ("Tier A per-node schedd and placement", Phase 2 / Gate A,
PR #509) shipped every mechanism to make a multi-box fleet work —
`apps.node_id` FK, `compute_nodes.schedd_target_url`, owner-scoped
reaper / cron / watchdog loops, the gateway's per-node schedd client
cache, `authorizeApp` / `authorizeInstance` ownership guards — except
one thing: when `compute_nodes.active` flips to `false`, the apps
pinned to the dead node stay pinned. Subsequent wakes on those apps
fail with `codes.FailedPrecondition` from
`pkg/scheddgrpc/ownership.go:54` and customers see a 503 until the
operator manually re-stamps the apps.

ADR-062's §"Open follow-ups (deliberately deferred)" calls this out
explicitly as item 1. The runbook pre-PR also called it out
(`docs/runbooks/gate-a.md` §"Rollback"):

> "Do NOT flip `apps.node_id` rows manually — the chooser would just
> re-stamp them on the next cold start. Leave the rollback at 'one
> schedd serves everyone via default-local' and triage in a follow-up
> PR."

Tier A4 closes that gap.

## Decision

### Trigger

`pkg/sched/rebalancer.go` subscribes to
`db.NotifyComputeNodesChanged` (post-00276; was
`db.NotifyComputeNodeChanged` before the channel split, payload
`{"node_id":uuid,"active":bool}` unchanged) and filters to
`active=false` events. On each match, calls
`Engine.RebalanceOrphanedApps(ctx, deadNodeID)`. Shape parallels
`pkg/sched/router_watcher.go` (drain goroutine + typed JSON
filter + per-payload work closure). The rebalancer is the fourth
consumer of `compute_node_changed` on schedd (alongside the router
watcher, nodekeys refresh, and nodeVerifier mTLS-epoch advance).

### Eligibility

Reassignment only for apps in `apps.status` ∈ {`active`,
`evicted_cold`} — the non-deleted, non-stationary states. A
`running` / `waking` / `cold_booting` / `snapshotting` instance on
the dead node is **not** stolen; that node must drain cleanly first
(active-passive HA, already covered by the runbook). The
rebalancer is a parked-app re-owner, not a VM-evictor.

### Atomicity

`Store.ReassignAppOwner(ctx, appID, fromNodeID, toNodeID) error` whose
SQL is

```sql
update apps
   set node_id = $3, reassigned_at = now()
 where id = $1 and node_id = $2
   and status in ('active', 'evicted_cold')
```

`RowsAffected() == 0` → `state.ErrConflict` (peer-claimed, app moved
to live, or app gone). The `fromNodeID` predicate makes a
peer-claim-then-second-peer-claim race never silently succeed with a
stale from-node.

### Pacing

`api.RebalanceCooldownSeconds` (default 60s, env override
`FAAS_REBALANCE_COOLDOWN_SECONDS`) suppresses repeated reassignments
of the same app to prevent a flap-loop. The "last reassigned at"
timestamp lives on the `apps` row via a nullable column
`apps.reassigned_at` (migration 00092). The `ListOrphanedApps` SQL
filter only returns apps where
`reassigned_at IS NULL OR reassigned_at < now() - interval '<n>s'`.

### Admission

Before any UPDATE the rebalancer reads
`compute_node_used_mb(owner_node_id)` once up-front, then decrements
by the per-app RAM as we succeed/fail to keep the cap honest within
the batch. If `used + ram_mb + PerVMOverhead > admission_ceiling_mb`
the app is dropped from this batch (leaves it pinned to the dead
node — next heartbeat-staleness sweep will retry). One-time log per
batch at Info, breakdown per outcome at the metric.

### Concurrency

`api.RebalanceMaxPerTickPerNode` (default 50, env override
`FAAS_REBALANCE_MAX_PER_TICK`) caps the per-drain-event batch so a
5,000-app orphaned node doesn't monopolise the schedd worker pool.
Excess apps stay pinned; the next `compute_node_changed` re-fires.

### Observe

- Metric: `schedd_rebalance_decisions_total{outcome=…}` with closed
  set ∈ {`migrated`, `conflict`, `no_headroom`, `cooldown`,
  `no_eligibility`}.
- Notify: `db.NotifyAppChanged` payload
  `{"kind":"rebalanced","app_id":"…","from_node":"…","to_node":"…"}`
  after each successful UPDATE commit.

### Wire

None. All consumer changes are server-side (ADR-016 holds).

### Cold-start sweep

A schedd that was down while a drain event landed recovers via a
one-shot goroutine at startup:
`Engine.RebalanceOrphanedApps(ctx, "")` — empty `deadNodeID` means
"every orphan, regardless of which dead node owned it". Mirrors the
post-00091 cold-start sweep at `cmd/schedd/main.go`.

## Migrations

- `migrations/00092_apps_reassigned_at.sql` — additive nullable
  `reassigned_at timestamptz` column + `apps_reassigned_at_chk`
  CHECK tolerating clock skew + partial index
  `apps_reassigned_at_idx WHERE reassigned_at IS NOT NULL`.
- `migrations/00093_apps_node_reassignable.sql` — partial composite
  index `apps_node_id_status_partial_idx ON apps (node_id, status)
  WHERE node_id IS NOT NULL AND status IN ('active',
  'evicted_cold')` used by `ListOrphanedApps` to skip the full
  apps scan.

## Known downstream effects

- **Snapshot-key orphaning.** `vmstateStorageKeyFor`
  (`pkg/sched/engine.go:1768`) keys snapshots by
  `(nodeID, deploymentID)`. A rebalanced app's new owner writes
  snapshots under a fresh key; the dead node's snapshot remains on
  disk until the `runDiskDrift` sweep reaps it. Not a correctness
  issue (cold-boot from disk works) but disk usage may briefly
  spike during a large rebalance.
- **Heartbeat-staleness watchdog** (issue #97 §3, **not in this
  PR**) is the upcoming source of automated `active=false` flips;
  the rebalancer consumes the same `compute_node_changed` channel
  and will pick up watchdog-triggered drains without a code change.

## Open follow-ups (deliberately deferred to Tier A5+)

- Cross-node live-instance migration (`running` / `waking` / etc.
  on the dying node). Active-passive HA + per-node schedd shutdown
  ordering handle the realistic ops use-case; live migration would
  need a new VM-handoff primitive.
- Per-node schedd active-passive control-plane HA — orthogonal;
  heartbeat-staleness watchdog lands separately (issue #97 §3) and
  reuses Tier A4's rebalance watcher unchanged.

## Rejected alternatives

- **Leader election per drain event.** Adds an etcd / pg_advisory
  dependency for every active=false event. The conditional UPDATE
  already gives exactly-one-wins-per-app via
  `WHERE node_id = $from`. Two schedds losing one Postgres race is
  cheaper than two schedds reaching consensus.
- **Migration at the egress-drift layer.** Pulled app rows for
  every active schedd to rewrite; the apps table is the source of
  truth for owner, not the egress-allowlist fan-out.
- **Advisory locks around the whole orphan set.** A second-tier
  flap-loop defence but doesn't add anything the
  WHERE-node_id-=from predicate doesn't already provide; the
  per-app cooldown at the SQL filter is the only thing that
  matters.
- **Tier-A5 stream orchestrator** (issue #525). Out of scope; this
  ADR is apps-only, no stream-cluster reassignment, no gatewayd
  pool reshuffle.

## Implementation

- `pkg/sched/rebalancer.go` (new) + `pkg/sched/rebalancer_test.go`.
- `pkg/sched/engine.go` `Engine.RebalanceOrphanedApps`.
- `pkg/state/{store,types,pgstore,memstore}.go`
  `ListOrphanedApps` + `ReassignAppOwner` + `App.ReassignedAt`.
- `pkg/api/limits.go` `RebalanceCooldownSeconds` +
  `RebalanceMaxPerTickPerNode`.
- `pkg/wire/metrics.go` `RebalanceDecisions(outcome)` accessor.
- `cmd/schedd/main.go` `subscribeRebalancer` seam +
  cold-start sweep + `FAAS_REBALANCE_*` env reads.
- `docs/runbooks/gate-a.md` §"Compute eligibility" rewrite
  + §"Rollback" caveat drop.
