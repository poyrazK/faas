# ADR-20261121203000: Return drained apps to their home node

- **Status:** proposed
- **Date:** 2026-11-21
- **Amends:** ADR-20261118160000 (ownership balance)
- **Related:** ADR-20261022184531 (node wake failover), ADR-20261028200212
  (parked-only owner moves), ADR-20261110210000 (owner authority), ADR-421
  (ownership recovery)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A rolling compute rollout drains one node at a time. Ownership recovery
moves every app off the draining node onto the peer (the node is no longer
admitting; ADR-20261022184531 uses the same path for a node that dies). On
2026-11-21 bhs-2 drained and then bhs-3, so bhs-2 ended up owning all 91 apps
(60.9 GB) and bhs-3 owned none.

The ownership balance (ADR-20261118160000) did not repair this. It moves only
apps that were neither woken nor moved for `api.OwnerBalanceMinIdle` (24 h),
and the drain had just stamped `apps.reassigned_at` on every app. For 24 h,
then, every new app went to the empty node (`ChooseOwnerNode`), every wake of
an existing app hit bhs-2, and the release-acceptance placement gate
(`gregalectl release-acceptance verify-placement`) could not cover both
nodes. An operator moved 40 parked apps by hand.

The 24 h guard exists so a busy app keeps the owner whose cache is warm. A
drain move does not serve that purpose: the warm cache is on the node the app
was pushed *off*, and the move was forced, not chosen.

## Decision

1. **A displacement remembers home.** A new nullable column,
   `apps.owner_home_node_id` (FK to `compute_nodes`, `ON DELETE SET NULL`),
   is written only by `ReassignOrphanedAppOwner`, the move ownership recovery
   makes when a drain or a dead node takes an app's owner away:
   - no home yet: the home becomes the node the app was moved off;
   - an older home is kept across a chain of displacements;
   - a displacement that lands the app on its home clears it.

   Every other ownership move (`ReassignParkedAppOwner` for balance, pressure
   and return moves, and `ReassignAppOwner`) clears it, because that move
   chose a new owner on purpose.

2. **Displaced apps go home first.** On each tick, before it balances,
   `OwnerBalancer` returns up to `api.OwnerReturnHomeMovesPerTick` (20) apps
   to their home. An app returns when all of these hold:
   - it was displaced within `api.OwnerReturnHomeWindow` (6 h);
   - its home is active, fully admitting (`lifecycle=active`; a recovering
     node is not an ownership destination, as in ownership recovery), not
     degraded or unavailable, and has a ceiling for one instance;
   - the home can restore its live snapshot (the balance's CPU rule).

   A tick that returned any app does not also balance, so the balance reads
   the load the returns left. Each move uses the same parked-only
   compare-and-set as the balance (ADR-20261028200212), so an app with a
   resident instance stays where it is serving and returns once it parks.
   Routers are notified (`app_changed`, kind `rebalanced`), and the moved
   snapshots are queued onto the home node, as in ADR-20261118160000 §2.5.
   The 91-app skew of 2026-11-21 is undone in about 5 minutes.

3. **A displacement does not hold an app for the balance.** An app whose
   last move was a displacement is a balance candidate if it has been idle
   for `api.OwnerBalanceMinIdle`, however recently it was displaced. The
   guard still applies in full to an app woken in the last 24 h and to an
   app the balance or pressure rebalancer moved in the last 24 h. This
   covers a home node that does not come back within the window, or is
   retired.

`FAAS_OWNER_BALANCE=off` turns off the return together with the balance.

## Consequences

- After a rolling release, each node gets its own apps back within minutes
  of becoming active again. The release-acceptance gate and wake spread no
  longer depend on a hand rebalance.
- A busy app is never moved while it has a resident instance. An app that
  was woken on the peer during the rollout and has since parked returns
  home, where its snapshot was cached before the drain (and is re-queued if
  evicted).
- An app displaced for longer than 6 h is treated as settled where it is;
  only the balance can move it, with its idle guard.
- One expand-only migration adds the column. Older binaries ignore it and
  keep the 2026-11-18 behaviour.
- The §6.2 invariants are untouched. Ownership moves never admit or start
  instances, and every move keeps the parked-only fence.
