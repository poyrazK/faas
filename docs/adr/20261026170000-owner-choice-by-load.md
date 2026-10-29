# ADR 20261026170000 · Choose a new app's owner by ownership load

- **Status:** proposed
- **Date:** 2026-10-26
- **Amends:** ADR-062 (schedd-side async placement claim)
- **Related:** ADR-421 (orphaned app recovery), ADR-087 (pressure rebalance)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-062 has every schedd race to claim a new app: each runs the instance
placement chooser and the first conditional `UPDATE apps SET node_id` wins.
That chooser biases toward the claiming schedd's own node, keeping it
whenever its CPU headroom ties the best. A new app has no instances, so
headroom usually ties, and the owner was whichever schedd answered the
`app_changed` notification first. Ownership is long-lived: the owner runs the
app's wakes, reaper decisions, crons and service replicas, and its node is
the preferred placement for the app's instances. A node that answered first
kept collecting apps while its peers stayed light.

## Decision

`ClaimUnplaced` chooses the owner by ownership load:

1. `AppOwnershipLoadReader.OwnedAppRAMByNode` sums `apps.ram_mb` over each
   node's non-deleted apps.
2. Among active, admitting nodes whose admission ceiling holds one instance
   of the app, the owner is the node whose owned RAM is the smallest share of
   its ceiling, then the lower name. A degraded node is chosen only when no
   other node qualifies.
3. Every schedd computes the same answer from the same rows, so whichever
   wins the race stamps the balanced owner.
4. When the store cannot report ownership load, or no node qualifies, the
   previous chooser runs unchanged.

## Consequences

- New apps spread across owners in proportion to node size, even while no
  app has woken yet.
- Ownership still does not move after the claim. Rebalancing remains with
  ADR-087 and ADR-421.
- Owned RAM is a declared size, not use: a node owning many idle apps looks
  as loaded as one owning busy ones. That matches what the owner commits to
  (it places those apps' instances first) and needs no telemetry.

## Validation

`TestChooseOwnerNode`, `TestClaimUnplacedPrefersTheLessLoadedOwner`, and
`checkOwnedAppRAM` on MemStore and PostgreSQL.
