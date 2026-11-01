# ADR 20261031190030 · Scheduler review fixes: thaws, frozen yield, retention

- **Status:** proposed.
- **Date:** 2026-10-31
- **Amends:** ADR 20261031143521 (proactive compression no longer the last
  word on a full node), ADR-1740 (which wakes a thaw ticket serves), ADR-643
  (park-to-admit's candidate order), ADR-595 (the runtime input guard's
  comparison), ADR-909 (a frozen instance's measured charge)
- **Related:** ADR-1151 (frozen tier), ADR-1162 (spend cap), ADR-193
  (durable per-node reservation), spec §17 (retention)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A review of `pkg/sched` on 2026-10-31, then a read-only check of production
(release `6733db534e`, frozen tier and fast path on, 24 h of logs and
metrics), found:

1. **Image rescans break thaws.** Each instance captures its runtime inputs at
   admission, including the deployment's `artifact.scan_status` and
   `scan_result_hash`. Every rescan (every 1-5 h in production) renews them.
   `application_standard_runtime_inputs_match` drops them only for native
   inputs, so the guard refused the next frozen-to-running or warm-to-running
   publication of every ordinary instance: 24 refusals since 10-29, about 5%
   of thaws, each after vmmd had already resumed the VM, which was then
   destroyed and restored.
2. **The fast path is unused.** The gateway names scope `production` for
   every project app; the ticketed thaw refused every non-empty scope. 258 of
   283 thaws in a day skipped it (reason `scope`) while 500 tickets were built.
3. **The fast path skips the spend cap.** The ordinary thaw honours it
   (Hunt 18); the ticketed thaw did not, nor did EnsureWake's fresh selection
   the fast path trusts.
4. **Nothing yields frozen memory to admission.** A frozen VM is unbilled and
   disposable, but its memory stays booked until FrozenTTL. Compression runs
   above the ceiling or, since ADR 20261031143521, on zswap nodes; a node
   without zswap kept refusing wakes, and park-to-admit parked other
   customers' running instances while frozen VMs stayed (37 times in 7 days,
   with 1-3 frozen VMs on the node).
5. **Compression reaches admission ten minutes late.** The measured charge
   holds each instance's p95 over 600 s, so a compressed frozen VM kept its
   pre-compression charge, and the proactive pass kept compressing more VMs.
6. **Retention fails forever on job instances.** `job_tasks.instance_id` has
   no delete action and job tasks outlive the 30-day instance retention: the
   same 11 rows failed every sweep, about 200 failures a day.
7. **Guard refusals are mislabelled.** The durable guard's vCPU and CPU
   refusals reached customers and operators as "RAM headroom".
8. **Floor headroom misses empty nodes.** It looked only at nodes the ledger
   held reservations on.
9. **Frozen VMs re-booked after a restart keep a 64 MB stand-in** charge,
   which nothing corrected.

## Decision

1. The runtime input guard leaves `artifact.scan_status` and
   `artifact.scan_result_hash` out of its comparison for every input
   (migration `20261101083902561`, and MemStore's equivalent). Captures keep
   their bytes; `artifact.parked_reason`, which a regressed scan sets, and the
   artifact itself stay compared.
2. A wake for scope `production`, `default` or none takes the fast path: a
   ticket is issued only for the deployment the production lane selects
   (FrozenThawBasis). Any other scope still decides from fresh reads.
3. The ticketed thaw checks the spend cap on every path, including a
   coordinated wake whose fresh selection marked admission checked.
4. Frozen VMs yield their memory to admission:
   - The reaper's density pass, after compression, parks a node's
     longest-frozen VMs (frozen at least `FrozenCompressMinAgeSeconds`, at most
     `FrozenYieldPerNodePerTick` = 8 a tick) while the node is at
     `FrozenCompressHighWaterPercent` and until it is under
     `FrozenCompressLowWaterPercent`, when compression has nothing left to try
     there or the node cannot compress. Park reason `admission`. It runs under
     `frozen_proactive_compress`.
   - A wake refused for node RAM parks, before any running candidate, at most
     `FrozenYieldPerRefusedWake` = 8 frozen VMs on the node where the fewest
     free what that node lacks, waits up to `FrozenYieldFreshViewMillis` for
     the node's measurement and capacity report to show it, then retries once.
     Park reason `refused_wake`. A refusal for another resource, or frozen VMs
     that cannot cover the request, fall through to the running candidates.
5. A frozen instance's measured charge uses its latest sample, not the
   window's p95: a paused VM cannot grow, and a thaw re-admits its full
   reservation.
6. The retention sweep leaves STOPPED/FAILED instances that a job task
   references; their rows stay with the job's history.
7. The durable guard's refusals name their resource: vCPU and CPU keep their
   own message and blocker (`node_vcpu`, `node_cpu`).
8. The floor's headroom is the best admitting node's, from the recovery
   runner's node list, falling back to the ledger before the first list.
9. A frozen reservation SeedLedger re-booked takes the first measured charge,
   up or down, once.

## Consequences

- Thaws survive rescans; the fast path serves production wakes, honouring
  the spend cap.
- On a full node frozen VMs go before running ones, so density no longer
  depends on zswap; a parked frozen VM's app restores on its next request
  (ADR-005), as after FrozenTTL.
- No customer-visible billing change: frozen time stays unbilled.
- The migration only replaces a function; an older scheduler is unaffected.
