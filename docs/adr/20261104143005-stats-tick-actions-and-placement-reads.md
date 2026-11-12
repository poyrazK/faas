# ADR 20261104143005 · The stats tick's actions off the loop, cached placement CPU reads, a per-node ledger index

- **Status:** proposed.
- **Date:** 2026-11-04
- **Amends:** ADR-191 (the bounded off-loop work pool), ADR-20261023171905
  (the quiet-pause ledger), ADR-909 (measured admission)
- **Related:** ADR 20261104091530 (cron, recent load, operator intents and
  heartbeat off the loop)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

An eleventh review of `pkg/sched` found:

1. **The 200 ms stats tick destroyed VMs on the select loop.** The
   instance-stats poller runs on the loop. Its disk-pressure and
   egress-abuse enforcement called `RecycleForDiskPressure` and
   `RecycleForEgressAbuse` inline, one instance after another. Each takes the
   app lock (held through a waking instance's boot), marks snapshots stale
   and destroys the VM with a 5 s budget. CPU-saturation captures wrote to
   Postgres there too. The quiet-pause ledger loaded and flushed on the tick,
   up to 2 s per call.
2. **Every wake ran two uncached aggregate queries.** Placement read booked
   CPU and booked vCPU with aggregates over the candidate nodes' live
   instances on every wake; only the memory read went through the 1 s
   `NodeUsageCache`.
3. **Placement scanned the whole ledger once per candidate node.** Measured
   admission adds a node's reservations admitted after its last measurement,
   and `admissionUsed_locked` found them by scanning every reservation in the
   fleet, under the mutex every admission and release takes. Placement asks
   it for each candidate node, and `Admit` also scanned every reservation to
   expire CPU startup boosts.
4. **Two engine maps never shrank.** `serviceMu` kept a mutex for every
   deployment ever reconciled and `serviceAppMu` one for every app.
   `restartCompleted` kept one wake ID for every app ever restarted.

## Decision

1. **The tick's per-instance actions run on the work pool.** The new kind
   `instance_enforcement` has 4 slots, is keyed by action and instance, and
   drops on overflow. An action is recorded as taken only when it succeeds,
   so the next tick resubmits a dropped one while the condition holds.
   **The quiet-pause ledger runs on its own worker.** Every report reaches it
   in tick order: billing depends on it, and the ledger skips a report older
   than one it has applied. A full queue (64 reports) makes the tick wait
   rather than drop.
2. **Booked CPU and vCPU go through 1 s caches,** like booked memory. The
   local ledger's own bookings are still read fresh and win when higher.
   Admission and the durable node reservation still decide.
3. **The ledger indexes reservations by node and keeps the boosted ones
   apart.** Measured admission, the frozen counts per node and boost expiry
   walk only those. Every insert and delete goes through one pair of helpers.
4. **Service locks are dropped once nobody holds or waits for them,** like
   app locks. A completed restart's wake ID is remembered for an hour (the
   duplicate-delivery window), pruned at most once a minute.

## Consequences

- The select loop no longer waits on recycles, captures or ledger writes.
  The tick can only wait when the ledger worker is 64 reports behind.
- Placement may see another schedd's CPU bookings up to 1 s late, as it
  already did for memory. This schedd's own bookings are current.
- The ledger's per-wake work grows with one node's reservations, not the
  fleet's.
- schedd's memory no longer grows with every deployment and restart.
