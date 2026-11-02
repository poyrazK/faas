# ADR 20261102072403 · A thaw passes the durable node reservation; node-local reads; early freeze on

- **Status:** proposed.
- **Date:** 2026-11-02
- **Amends:** ADR-193 (the durable per-node reservation now covers a thaw),
  ADR-1151 (a frozen VM's return to service), ADR-20261021215353 and its
  amendment ADR-20261023171905 (early freeze on by default, the reason
  recorded below), ADR 20261101091815 (the stats poller's node read)
- **Related:** ADR-909 (measured admission, invariant 2's 2× bound),
  ADR-1740 (ticketed thaw)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A third review of `pkg/sched` found:

1. **A thaw skipped the durable node reservation.** `admission_reserve_node`
   sums the live rows on a node and leaves frozen rows out, and the node guard
   (`instance_node_admission_guard`) returns early for a same-node update
   between live states. A thaw (FROZEN → RUNNING) therefore put the VM's plan
   RAM, vCPU and CPU back on its node with only the thawing scheduler's ledger
   checking, and that ledger counts only its own bookings. On a node holding
   several schedulers' apps the node's bookings could pass invariant 2's
   bound (Σ(ram_mb + 8) ≤ 2× the ceiling under measured admission). Since
   ADR 20261031190030 the ticketed thaw serves production wakes, so this is
   the common wake path.
2. **The stats poller read the whole fleet five times a second.** In the
   telemetry path (production) it listed every live instance in the fleet
   every 200 ms and kept its node's in Go: at a thousand instances a node and
   three schedulers, about 45,000 rows a second from the control-plane
   database. The node index `instances_live_node_id_idx` leaves out frozen,
   snapshotting and migrating rows, which are most of a dense node.
3. **`fcvm_resident_ram_pct` was meaningless on a multi-node fleet**: this
   scheduler's bookings over every node divided by the box-wide 47,600 MB,
   about 10% on production's 8,381 MB nodes whatever their load.
4. **Early freeze was off.** An idle function stays RUNNING, quiet-paused,
   until its 30 s idle timeout, holding its vCPU, CPU and concurrency
   bookings; under measured CPU admission those bookings count toward the
   700% cap that binds first on a dense node.

## Decision

1. **A thaw's publication runs the durable node reservation.** The scheduler
   binds the thaw's allowance and compute (`state.WithThawAdmission`: the
   ledger's reservation percent and CPU discounts, the app's vCPU and CPU
   millicores), and the publication calls `admission_reserve_node` for the
   frozen row's node inside its transaction, before the row leaves the frozen
   state: the ordinary thaw (`PublishOwnedInstanceRuntime` from FROZEN), the
   ticketed thaw (`ThawFrozenInstance`, now a transaction) and the rehearsal
   resume. No placement generation binds it (a thaw keeps its node); the node
   must still admit. MemStore mirrors it. A refusal parks the resumed VM with
   reason `no_capacity` and the wake places a fresh instance, as a refused
   ledger re-admission already did.
2. **The poller reads its node.** `ListResidentInstancesOnNode` reads the live
   instances on one node through a new partial index,
   `instances_resident_node_idx` (built concurrently), covering every state
   that holds memory on a node. Stores without it keep the old read.
3. **`fcvm_resident_ram_pct` reports the scheduler's node**: Σ(ram_mb + 8) of
   every owner's resident instances there, from the memory shadow, over the
   node's admission ceiling. A single-box scheduler keeps its ledger over the
   box-wide ceiling.
4. **Early freeze is on**, `frozen_tier_early_freeze_after_s` defaulting to
   `FrozenEarlyFreezeDefaultSeconds` (15 s); 0 turns it off. ADR-20261023171905
   keeps early freeze off "unless an operator wants earlier memory reclaim,
   with that reason recorded". The reason: on a dense node the vCPU, CPU and
   concurrency an idle `between_requests` instance holds until its idle
   timeout bind admission first; frozen, it holds none, and its memory joins
   compression and frozen yield.

## Consequences

- Invariant 2's bookings bound holds through thaws on shared nodes. A thaw
  costs one more statement in its transaction, and the ticketed thaw becomes
  a transaction (a few round trips instead of one).
- A function request arriving 15-30 s after the previous one thaws (fast path
  p50 53 ms, p90 89 ms on production-us) instead of resuming from a quiet
  pause (1-3 ms). Billing does not change: quiet pause is already unbilled.
- The poller's read scales with its node, not the fleet.
