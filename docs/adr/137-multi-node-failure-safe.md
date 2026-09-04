# ADR-137 · Multi-node failure-safe control plane

- **Status:** accepted
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [issue #1184 Workstream B](https://github.com/onebox-faas/faas/issues/1184) — "Make multi-node compute failure-safe".

## Context

Gregale runs a single control-plane node today. The architecture
targets multi-host (§14.A Workstream B), but a node crash today
exposes three operational gaps:

1. **No automatic recovery.** When a vmmd or schedd daemon dies,
   its live instances are stranded. The dead-node reconciler
   marks them `failed` so the customer stops paying (CLAUDE.md
   invariant 2: billing uses plan RAM + 8 MB per running second),
   but nothing migrates them to a healthy peer — the customer
   has to redeploy.
2. **No cascade drain.** Operators cannot safely retire a node
   for upgrades; the only existing path is `DELETE
   /v1/compute-nodes/{name}` which destroys live instances.
3. **Stale-route window.** The public gateway's split-box
   dialer (cmd/gatewayd-public/compute_gateway_pool.go) refreshes
   its endpoint snapshot only on TTL (5s) + dial (2s) — up to
   7s of stale routes after a drain or activation.

This ADR records the four load-bearing decisions made while
closing Workstream B.

## Decisions

### Decision 1 · `active` becomes a STORED GENERATED column

`compute_nodes.active` is no longer an independent column.
Migration 00579 adds a `lifecycle` enum column and a STORED
GENERATED `active` column derived from it:

```sql
ALTER TABLE compute_nodes
  ADD COLUMN active BOOLEAN
  GENERATED ALWAYS AS (lifecycle IN ('active','recovering')) STORED;
```

**Why:** the legacy `active` column was queried from three
seams (placement filter, pg_notify consumer, scheduler
fan-out). Replacing every reference with `lifecycle IN (...)`
would scatter the same predicate across the codebase. The
generated column preserves every existing query + index
unchanged, while the new enum column carries the four-state
machine semantics (`active | draining | unavailable |
recovering`).

**Trade-off accepted:** the WHERE clause on the partial index
`compute_nodes_lifecycle_idx` must be IMMUTABLE. `lifecycle IN
(...)` against an enum column is immutable, verified by the
pgtest at `pkg/state/pgstore_lifecycle_test.go`. The
`CREATE INDEX CONCURRENTLY` path on a generated column requires
PG ≥ 12 (the migration gate enforces this).

### Decision 2 · Four-state lifecycle (not five)

Issue #1184's text proposes five states (`active`, `draining`,
`unavailable`, `recovering`, `failed`). This ADR collapses to
four: `recovering` is a sub-phase of `active`, not a peer.

**Why:** a node in `recovering` still admits new wakes
(placement filter accepts it). Splitting it from `active`
would force the placement filter to enumerate both, and the
generated `active` column (`lifecycle IN ('active',
'recovering')`) already captures the contract. `failed` is
not a lifecycle state — it's a terminal instance state on a
row that lives on a node (covered by the existing `instances`
state machine, ADR-137's recovery_recreate path).

**Trade-off accepted:** operators querying `lifecycle` directly
get four values, not five. The dashboard's filter surface gets
a clearer contract.

### Decision 3 · Recovery arbiter as single decision policy

The arbiter (`pkg/sched/recovery_arbiter.go`) is the only
component that decides migrate-vs-recreate. The existing
`live_migrator` + `deadnode_reconciler` become downstream
consumers (Tasks #59, #60, #61).

**Why:** before this ADR, both paths ran independently with
their own per-tick decision logic and raced on the same
instance row. A failing healthcheck could trigger both a
live-migration enqueue AND a recreate decision; the row's
recovery_kind column would land on whichever won the CAS.

The arbiter collapses the decision into one tick:
`Arbiter.Tick(ctx)` reads `NodeListRecoverable`, calls
`Decide(ctx, node, instance)` for each live instance, and
dispatches to the chosen handler. Per-instance race safety
comes from the existing `instances.lease_token` CAS (the same
mechanism live-migration uses today).

**Trade-off accepted:** the arbiter adds one goroutine to
cmd/schedd's main loop (1s ticker). The decision logic is
small (8-case table-driven test at
`pkg/sched/recovery_arbiter_test.go`). The downstream path
fan-out is bounded by the existing migration concurrency
ceiling (`MigrateLiveMaxPerTick`).

### Decision 4 · Snapshot-miss backoff stamps, not HTTP retries

The wake path's snapshot-cache-miss branch now stamps a
per-deployment `snapshot_miss_count` + `snapshot_miss_backoff_until`
(migration 00585) and surfaces a `Retry-After` header to the
gateway (Task #64).

**Why:** under sustained misses (FC upgrade in flight, image
registry unreachable, deployment misconfigured) the wake
hot loop was burning RAM + capacity indefinitely without
making progress. Capped exponential (5s base × 2^n, 300s
max, 6 attempts before freezing) collapses the worst case
to one cold-boot per 5 minutes per deployment — bounded
blast radius.

**Trade-off accepted:** a deployment that legitimately lost
its snapshot row waits 5 minutes for the next attempt.
Operators observe via the `snapshot_fleet_avg_mb` alert +
the `deployment_audit.snapshot_miss_count` column. Cold boot
is always available (ADR-005); the backoff is on the
snapshot-cache lookup, not on the wake itself.

## Out of scope

- **Multi-apid HA** — Tier A8. Out of scope for Workstream B.
- **Service-mode execution** — Workstream C; the
  `pkg/sched/recovery_arbiter.go::Tick` API is designed so
  Workstream C's service reconciler can call it on
  replica-loss events without rewriting the decision logic.
- **Snapshot replication factor > 1** — Tier A6; the
  `SnapshotReplication >= 1` predicate in the arbiter's
  decision table picks up this signal as soon as the
  replication write lands.

## Cross-references

- Issue #1184 Workstream B (umbrella EPIC)
- Migration 00579 (`compute_nodes_lifecycle`), 00582
  (`compute_nodes_recovery_audit`), 00585
  (`deployments_snapshot_backoff`)
- `pkg/sched/recovery_arbiter.go` (decision policy)
- `pkg/sched/recreate.go` (Engine.RecreateInstance primitive)
- `pkg/sched/snapshot_backoff.go` (Retry-After surface)
- `cmd/e2e/twonode_failure_safe_metal_test.go` (acceptance
  tests)
- `docs/runbooks/twonode-fault-injection.md` (operator drill)
