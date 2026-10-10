# ADR-972: Per-plan rollback history

Status: accepted · 2026-10-10

## Context

Spec §4.6 and the prime-snapshot step fixed rollback retention at the newest
three deployment generations per app: the live deployment and two previous
ones. imaged's nightly GC (`perAppKeepRollbackWindow`) enforced it with the
single constant `SnapshotRollbackRetentionDeployments = 3`. Once a generation
leaves the window, GC deletes its snapshots. It then deletes the generation's
cold-boot layer, because a superseded deployment no longer holds a layer
reference. Rollback to that generation then fails with
`rollback_target_unavailable`.

That limit was the same on every plan and appeared nowhere in customer docs.
The constant's comment also promised a cold-boot fallback for older releases.
No such fallback can exist once the layer has been reclaimed.

Managed platforms expose this as a version-lifecycle setting (Elastic
Beanstalk keeps a bounded number of application versions). Gregale's cost
structure points the same way: Free apps should hold less parked material,
and Scale, the regulated tier, should offer deeper rollback.

## Decision

Rollback history becomes a per-plan limit, `Limits.RollbackRetentionDeployments`
in `pkg/api/limits.go`. The count includes the live deployment.

| Plan | Rollback history |
|---|---:|
| Free | 2 |
| Hobby | 3 |
| Pro | 3 |
| Scale | 5 |

Pro keeps the previous fleet default, so existing GC behaviour for the
paid-production tier does not change.

imaged's GC resolves each account's plan once per GC tick and applies that
account's window, through `perPlanKeepRollbackWindow` and
`evictOldestFromHeaviestAccountWithDepth`. Every app belongs to exactly one
account, so partitioning rows by account keeps the existing per-app and
per-environment generation grouping unchanged. Disk-pressure eviction honours
the same per-plan window.

If an account cannot be read or has an unknown plan, GC uses the deepest
window across all plans (`MaxRollbackRetentionDeployments`). A lookup failure
can therefore never reclaim a generation that some plan would keep.

`SnapshotRollbackRetentionDeployments` remains only as the fallback for pure
helpers that have no plan context. Its comment now states the real behaviour:
there is no cold-boot fallback past the window.

## Consequences

- The generated plans page gains a "Rollback history" column. The rollback
  docs describe the limit, the `rollback_target_unavailable` outcome and
  redeploying the source as the recovery. The API error detail names the
  limit.
- A plan downgrade trims history at the next nightly GC. An upgrade cannot
  restore generations that were already reclaimed.
- Free apps hold one fewer parked generation, saving roughly one snapshot
  (about 200 MB of real content as of 2026-10-09) per frequently redeployed
  app.
- Scale apps may hold two more generations. The worst case is about 400 MB of
  snapshot content per Scale app that redeploys often. Account-fair pressure
  eviction against the 452 GB budget still bounds the fleet, but it now
  protects the deeper Scale window too. Watch `snapshot_fleet_avg_mb`
  (target 130 MB) after rollout.
- Customers cannot see before attempting a rollback whether a given revision
  is still restorable. A per-revision `rollback_available` field in the
  deployment API and CLI is a follow-up.

## Rejected alternatives

- **Keep a single fleet constant.** Free pays for history it rarely uses, and
  Scale gets no rollback depth to match its tier.
- **Keep layers but drop snapshots past the window, so rollback cold-boots.**
  This makes the documented fallback real, but every reclaimed generation
  would keep its layer forever. That reintroduces unbounded storage per app
  and still needs its own limit.
- **Time-based retention, e.g. 30 days.** An app that redeploys often would
  keep unbounded history inside the period, and a stable app would lose
  rollback to its only previous release. A count matches how rollback is
  used.
- **Customer-configurable depth.** This adds a setting before any customer has
  asked for one. Plan entitlement covers the need and keeps disk budgeting
  predictable.

## Validation

- `pkg/api`: per-plan values, the deepest-window helper and unknown-plan
  handling (`TestRollbackRetentionDeploymentsPerPlan`), plus the plan golden
  tables.
- `pkg/imaged`:
  - per-account windows in one GC pass;
  - pressure eviction inside and outside a window;
  - plan resolution against `MemStore`, including missing and empty accounts;
  - the existing pressure-mode loop test, with its heavy account moved to Pro
    so its four generations still exceed the window.
- `cmd/pricing-md`: generator test and `pricing-check` drift gate.
- The change does not alter VM lifecycle, so the metal acceptance gates are
  unaffected.
