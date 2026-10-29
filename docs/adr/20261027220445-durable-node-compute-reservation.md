# ADR 20261027220445 · The durable node reservation bounds vCPU and CPU

- **Status:** proposed
- **Date:** 2026-10-28
- **Amends:** ADR-193 (scope of the durable per-node reservation)
- **Related:** ADR-062 (owner shards), ADR-062 amendment 2 (fleet vCPU
  placement), ADR-909 amendment 1 (scheduler-bound allowance), ADR-1151
  (quiet pause admission), ADR-1741 (one-statement admission)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-193 put a durable floor under the per-node RAM ceiling: the headroom
check and the instance insert share one transaction holding a per-node
advisory lock, so two owner schedds cannot both claim a node's last
megabytes. It left the node's vCPU budget (`compute_nodes.vcpu_budget`) and
physical CPU budget (`vpcpus × 1000 × api.CPUOvercommit`) to
`sched.NodeLedger`. Since ADR-062 amendment 2, placement reads every owner's
vCPU and CPU bookings from the store, but the ledger's final check still
counts only its own. Two owners that read a node's vCPU or CPU headroom at
the same moment can both admit, and the node ends up overbooked. Under a
burst the risk grows, because every owner ranks the same least-loaded node
first.

The obvious durable check, the node's raw bookings plus the request against
the budget, would refuse admissions placement allows. Under quiet pause
admission (ADR-1151), placement and the ledger discount instances the latest
vmmd sample saw quiet-paused, and that sample is not in the database.
ADR-193 forbids a guard stricter than the chooser: a node placement believes
has room would refuse every wake routed to it.

## Decision

1. A scheduler's placement binding (`state.NodeAdmission`, ADR-1528) also
   carries the admission's vCPU and CPU millicores, which are placement's
   request, and the quiet-pause discount the ledger applied to that node
   (`NodeLedger.QuietDiscount`). Every binding a scheduler makes carries
   them: wakes, scale-outs and restores through `createWithNodePlacement`,
   job tasks, and qualification admissions.
2. Under the node lock it already holds, the reservation sums the node's
   live instances exactly as placement's fleet aggregates do: `capacity_vcpu`,
   and each app's CPU with an active startup boost. It then refuses with
   `ErrNodeCapacity`, naming the resource, when bookings − discount +
   request exceed the budget. A zero request or budget is not checked, as
   in placement.
3. The guard bounds each discount by `api.QuietPauseMaxVCPUOvercommitPercent`
   of the budget. A scheduler can therefore never book a node past the
   ADR-1151 cap, whatever it reports. That is the same trust boundary
   ADR-909 amendment 1 drew for the RAM allowance.
4. Migration reservations are not in the guard's CPU sum. They stay with
   the migration admission (ADR-066), and leaving them out keeps the guard's
   sum at or below placement's.
5. Where the check runs: in `admission_reserve_node` and
   `admit_wake_instance`, new overloads from migration `20261027220445082`.
   The statement-by-statement path and MemStore mirror them.

Why the guard never refuses what placement admits: placement's effective
usage is `max(bookings − d, bookings − B × (cap − 100)/100)`, and its
bookings are at least the guard's. Admitted means effective + request ≤ B,
so guard bookings − min(d, B × (cap − 100)/100) + request ≤ B, which is the
guard's condition.

## Request path

No control-plane round trip is added to any request or wake:

- Requests to a running instance never reach schedd or Postgres.
- A wake's admission is still one statement (ADR-1741). The vCPU and CPU
  sums run inside it, under the lock it already takes, over the same
  per-node rows as the RAM sum. The discount comes from telemetry schedd
  already holds; it is a parameter, not a read.
- The frozen fast-path thaw does not go through node admission and is
  unchanged.
- The statement-by-statement fallback adds the sums to its existing
  headroom query.

The only extra work comes with a refusal. The scheduler then re-places on
another node, as it does for a RAM refusal (ADR-909 amendment 1).

## Rollout

The previous signatures delegate with a zero request, so the previous
release's admissions are unchanged. A schedd that starts before the
migration runs finds no new functions and admits through the statement path,
which applies the same check.

## Validation

- `checkNodeComputeReservation` runs on MemStore and on PostgreSQL, through
  both the function and the statement paths. It covers filling to each
  budget, admitting beside a quiet-paused instance, clamping an oversized
  discount to the ADR-1151 cap, and an admission without compute being
  checked against RAM alone.
- `TestDurableReservationRefusesUnseenVCPU` (sched): the owner node is full
  of instances the ledger has not seen, with the fleet aggregate hidden as
  it is in a race. The guard refuses the owner node and the wake lands on
  the peer.
