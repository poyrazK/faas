# ADR 20261101091815 · Node memory reports: measured admission across nodes

- **Status:** proposed.
- **Date:** 2026-11-01
- **Amends:** ADR-909 (measured admission, now on peer nodes too; missed
  samples hold an instance's recent charge), ADR-1151 (`FrozenMaxPerNode`
  counts the node, not one owner), ADR 20261031143521 and ADR 20261031190030
  (compression, frozen yield and eviction act on peer nodes)
- **Related:** ADR 20261030100950 (the CPU pressure backstop shares a node's
  verdict the same way), ADR-193 (durable per-node reservation)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Each compute node's vmmd streams its capacity report and instance stats only
to its own scheduler (`unix:///run/faas/schedd.sock`), and that scheduler's
stats poller keeps only its node's instances. Measured memory admission
(ADR-909) therefore had a measurement for a scheduler's own node and none for
a peer. Placement is CPU-headroom first, with the owner's node only a tie
break, so in a two-node fleet about half of an owner's wakes go to the peer.
There:

- placement compared the node's plan-RAM bookings, every owner's, with 100%
  of its ceiling;
- the ledger counted only its own bookings on the node, and its
  `ReservationPercent` gave the durable guard 100%, not ADR-909's 200%;
- eviction above the ceiling, proactive compression, frozen yield and
  `FrozenMaxPerNode` saw only the scheduler's own node or its own instances.

Once a node was booked past 100% of its ceiling, the normal state under
ADR-909, only its own scheduler could place there, however little memory it
used, and other owners' frozen VMs on it were never compressed or yielded.

Separately, any gap in a node's samples (a vmmd restart, a late stream batch)
charged every instance its full reservation; on a node booked past its
ceiling that refused every wake until samples returned.

A review of the park path also found that discarding a VM after a RAM or
runtime-config change released its reservation even when the destroy failed,
and that the stats poller's listing left out migrating VMs.

## Decision

1. **Each node's own scheduler publishes the node.** Every
   `NodeMemoryReportSeconds` (2) the scheduler that receives a node's vmmd
   stream upserts `node_memory_reports`: the node's measured charge (every
   owner's instances), vmmd's summed `memory.current` when fresh, its frozen
   VMs and when it was measured. A newer measurement is never replaced by an
   older one. Every scheduler caches the reports measured within
   `NodeMemoryPeerMaxAgeSeconds` (6).
2. **Peers admit by it.** Measured admission uses the local measurement for
   the own node and a fresh report for a peer, so a peer node gets the
   measured basis and the durable guard's 200% allowance. Placement on a node
   without a vmmd report (a peer, or a stream gap) uses the figure Admit uses,
   never below the node's total bookings less the overcommit allowance;
   without a measured basis it keeps the bookings as before.
3. **Pressure handling sees the node.** Eviction above the ceiling reads a
   peer's published used memory, so each owner evicts its own instances
   there. Compression and frozen yield act on peers through the measured
   basis. `FrozenMaxPerNode` counts the node's report or this ledger's own,
   the larger.
4. **Missed samples hold the recent charge.** An instance without a sample
   this tick keeps its recent charge (the latest sample and the window's
   p95; a frozen one its latest) for `MeasuredChargeHoldSeconds` (30), then
   falls back to its full reservation.
5. **A park discard keeps the reservation until the VM is gone**: a failed or
   timed-out destroy returns the error with the row RUNNING, as the
   ephemeral-secret discard already did.
6. **The live instance listing includes `migrating`.**

## Consequences

- A peer's view lags its own scheduler's by up to a few seconds: another
  owner's admissions there after the report are not in it. The durable
  guard's 200% cap on every owner's bookings stays the hard bound, and the
  node's own eviction handles any overshoot.
- Several owners may evict on the same report; at most one reaper tick of
  extra eviction before the next report shows it.
- One upsert per node every 2 s and one read per scheduler every 2 s on the
  control-plane database. A new table, so older schedulers are unaffected,
  and with measured admission off nothing is published.
