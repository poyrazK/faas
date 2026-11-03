# ADR 20261102160622 · The reaper reads in bulk; node-wide frozen worker cap; app locks are released

- **Status:** proposed.
- **Date:** 2026-11-02
- **Amends:** ADR-1750 (the strict compute sweep's reads), ADR-160 (the reaper's
  prewarm floor read), ADR 20261101091815 (FrozenMaxPerNode is node-wide for
  frozen workers too), ADR-20261019103000 (frozen worker pools)
- **Related:** ADR-191 (off-loop reaper work), ADR 20261102072403

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A fourth review of `pkg/sched` found:

1. **The reaper's reads grew with every app and instance it owns.** Every
   10 s, one query after another, the reaper's strict compute sweep listed
   each owned app's live instances and then read the latest compute lease of
   each resident instance, even for apps without a strict budget: every boot
   writes a lease, budgeted or not. The prewarm floor was one more query per
   app. With early freeze on (ADR 20261102072403) nearly every function app
   keeps a frozen VM for its
   idle timeout plus FrozenTTL, so at a thousand apps a node the tick ran
   thousands of round trips. A tick longer than its interval coalesces
   (ADR-191), so idle parks, frozen expiry and memory-pressure eviction fall
   behind as density grows. `schedd_loop_work_duration_seconds{kind="reaper"}`
   shows it.
2. **Frozen workers counted only their own scheduler's frozen VMs** against
   FrozenMaxPerNode. ADR 20261101091815 made the cap node-wide for frozen
   functions; the worker path kept the ledger's own count, so several
   schedulers could put more than FrozenMaxPerNode frozen VMs on one node.
3. **Per-app locks were never released.** The engine kept a mutex for every
   app it ever served, preview and deleted apps included.

## Decision

1. **The reaper reads the sweep's inputs once a tick.** One query lists every
   owned app's live instances (`ListLiveInstancesForApps`) and one reads the
   latest lease of every resident instance past its boot
   (`FinancialComputeLatestInstanceLeases`, through the existing
   `financial_compute_instance_history (instance_id, node_id, started_at DESC)`;
   no migration). The preload only rules instances out: before the sweep
   stops an instance it re-reads the lease and the row under the app lock, as
   the per-app read did, so a renewal after the tick's read still keeps a VM.
   An instance that appears after the tick's read is swept on the next tick.
   The prewarm floors of every owned app are one query
   (`ActivePrewarmFloors`). A store without these reads, or a failed bulk
   read, falls back to the per-app reads.
2. **Frozen workers use the node-wide count**: `nodeFrozenCount`, the larger of
   this ledger's frozen VMs and the node's published report.
3. **An app's lock leaves the engine when nobody holds or awaits it.** A
   reference count under the engine mutex tracks holders and waiters; every
   lock and unlock goes through `lockApp`, `tryLockApp` and `unlockApp`.

## Consequences

- The strict compute sweep costs two queries a tick plus, per owned app, its
  app row and its budget policies; a stop still costs its fresh reads.
- A budget stop for an instance woken during the tick waits for the next
  tick (10 s). vmmd's local deadline stays authoritative meanwhile.
- The per-deployment policy reads of the reaper's snapshot are unchanged and
  are the next lever if the tick is still long at density.
