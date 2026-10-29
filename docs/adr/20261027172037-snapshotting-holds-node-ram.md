# ADR 20261027172037 · A snapshotting instance holds its node's RAM

- **Status:** proposed
- **Date:** 2026-10-27
- **Amends:** ADR-193 (counted state set)
- **Related:** ADR-909 (measured admission), spec §6.2 invariant 2

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-193 made the durable per-node reservation sum the same states as the
placement chooser, `waking, cold_booting, running, draining, warm`, and left
reconciling that set with `State.CountsForRAM()` to its own ADR. Placement's
sum left out `snapshotting` on the reasoning that the instance is "parked from
RAM". Its VM is not: Firecracker pauses it and writes its memory out, and the
memory stays allocated until the capture finishes, up to
`SnapshotBudgetFor(ram)` (45 s at 1 GiB). Everything else that reasons about
resident memory already counts it:

- the ledger keeps a snapshotting instance's RAM booked (`BeginSnapshot`);
- `CountsForRAM` and therefore measured admission's charge include it;
- invariant 2 is about resident memory.

Only the database view disagreed, and that view is what other owners' schedds
place against. After an idle-park wave a node could hold many capturing VMs
while peers saw it as empty and filled it on top.

## Decision

1. Placement's node sum (`ComputeNodeUsedMB`, `ComputeNodeUsedMBByNode`, and
   MemStore's mirror) counts `snapshotting` rows.
2. The durable reservation sums the same set: `admission_reserve_node`
   (migration `20261027172037418`), the statement path and MemStore. The
   chooser and the guard still agree, as ADR-193 requires.
3. Unchanged: whether a new row takes the guard at all (no row is inserted
   `snapshotting`), migration resident extras, vCPU and CPU sums, and the
   operator views.
4. `migrating` and `frozen` stay out. Migration rows are booked through the
   migration admission and source tables; frozen rows book measured memory in
   the ledger (ADR-1151).

The existing `instances_recovery_node_live_idx` predicate covers the six
states, so the sums need no new index.

## Rollout

The previous release's admission function signature delegates to the
redefined one, so its admissions count capturing rows as soon as the
migration runs, while its chooser does not yet. A node that release sees as
having room can then refuse with `ErrNodeCapacity`. That refusal is correct
(the memory is in use), and since ADR-909 amendment 1 the scheduler re-places
it on another node.

## Validation

`checkSnapshottingHoldsNodeRAM` on MemStore and PostgreSQL (function and
statement paths) and `TestPlacementCountsCapturingInstances`.
