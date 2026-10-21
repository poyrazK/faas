# ADR-20261019103000 · Frozen worker pools

- **Status:** accepted; behind schedd `frozen_tier_workers`, off by default
- **Date:** 2026-10-19
- **Builds on:** ADR-1151 (pause tiers), ADR-906 (frozen instances),
  ADR-20261018101500 (worker pools off the scheduler loop), ADR-1140 (pull
  leases), ADR-138 (workers are not snapshotted)
- **Context:** A queue-driven worker pool that scales to zero cold-boots its
  first worker when demand returns. With ADR-20261018101500 and the booting
  token amendment of ADR-1141 that takes about 4 s on n2 to the first lease;
  most of it is the cold boot itself (disk staging, guest kernel, runtime).
  ADR-906 measured a Firecracker freeze and thaw at a few milliseconds, and
  ADR-1151 built a frozen tier for idle function and request-mode instances.
  That tier excludes workers twice: eligibility admits only request-serving
  workloads, and a frozen instance must have a snapshot to fall back on,
  which a worker never has. A worker pool also never idles out through the
  reaper; it scales in through its own reconcile, which stops the surplus
  worker.
- **Decision:**
  1. *Freeze on scale-in.* When a pool scales in, a surplus RUNNING worker of
     the live deployment freezes instead of stopping, when schedd's
     `frozen_tier` and `frozen_tier_workers` are on and it is eligible: it
     holds no unexpired queue lease (a frozen worker could not settle one),
     its app is active and on no private network, its RAM and runtime config
     are current, its captured config proof still matches the deployment's
     configuration (the thaw's config-fence check, made before freezing a VM
     that could never thaw), it has no ephemeral secret and no profiling, the app has no
     enabled push binding (the push path does not thaw a frozen worker), and
     the node is below `FrozenMaxPerNode`. The freeze is ADR-1151's: RUNNING →
     FROZEN, vmmd pauses the VM in place, the ledger books only a frozen
     charge. It stamps the freeze time in `last_request_at`, which a worker
     never otherwise sets, so FrozenTTL counts from the freeze. An ineligible
     worker, or one whose freeze fails, stops as before.
  2. *Thaw on scale-out.* When a pool needs workers it thaws its frozen
     workers of the live deployment first, the most recently frozen first,
     through ADR-1151's thaw (config-fence check, full re-admission, vmmd
     thaw, FROZEN → RUNNING publication), and boots new workers only for the
     rest. A frozen worker of a superseded deployment stops.
  3. *Stop, not park.* A worker has no snapshot (ADR-138): wherever the
     frozen tier would park a frozen instance (FrozenTTL, memory pressure,
     a failed thaw, a lost node, changed config), a frozen worker moves
     FROZEN → STOPPED, and the next demand cold-boots.
  4. *Worker slots.* A frozen worker is still resident and keeps its account
     worker slot: the store's worker reservation counts every resident
     worker, and a thaw is not re-checked against it. When a pool's demand is
     capped by `WorkerReplicasMax`, it stops the account's other frozen
     workers, the longest frozen first, to free the slots it needs.
- **Consequences:** A pool that scaled in within FrozenTTL plus the app's
  idle timeout resumes its worker instead of cold-booting it. Frozen workers
  are unbilled, as frozen instances are (ADR-906). A worker's in-process
  timers and connections pause while it is frozen, as ADR-1151 describes for
  functions; a long-poll lease call in flight at the freeze is answered or
  dropped by apid while the worker is paused, and the SDK retries it after
  the thaw. A workload token minted before the freeze may have expired by
  the thaw; the SDK fetches a new one. The switch is off by default, and
  turning it off drains frozen workers through thaws and FrozenTTL.
  An app's first pool worker usually cannot freeze: its cold boot records
  the workload class it observed (ADR-051) after publishing that worker's
  config proof, and the class is part of the configuration the proof
  covers, so the worker stops at scale-in and the next one freezes. The
  same ordering leaves that first instance's proof stale for every check of
  it (warm-pool promotion, the ADR-1740 fast path); it is not changed here.
- **Rejected alternatives:** *Keeping surplus workers RUNNING longer* (a
  longer scale-in stabilization window) keeps paying for an idle VM's vCPU
  and billing. *Excluding frozen workers from the account worker cap*
  would let a thaw add a running worker the store never re-checks, past
  `WorkerReplicasMax`. *Freezing workers through the idle reaper* would put
  a second owner on worker scale-in, which the pool reconcile owns.
- **Validation:** `pkg/sched` tests cover a surplus worker freezing, staying
  frozen through idle reconciles and thawing for demand without a cold
  boot; a surplus worker stopping with the switch off; and a frozen worker
  giving its slot to another environment's demand; and a worker whose boot
  changed the observed class stopping instead of freezing. The metal test
  `TestWorkerFrozenThawMetal` runs a real worker through freeze and thaw.
  It boots its own pool worker rather than waiting on the deployment's prime
  worker: on n2 a pool reconcile ran between Prime publishing the prime
  RUNNING and apid marking the deployment live, saw the prime as a retired
  generation and stopped it (a race on main, not changed here).
  On n2 (2026-10-21, revision 7eec5ca845) the test's first worker stopped
  at scale-in as above, the second froze, and three thaws took 685, 714
  and 815 ms from the send to the worker's RUNNING row and 792–850 ms to
  the message's lease, against about 4 s for a cold boot. vmmd's thaw took
  2–4 ms and schedd's thaw 29–39 ms; the rest was the wait for the pool's
  next 1 s reconcile tick. With a queue message waking an idle pool
  (ADR-20261018101500 decision 3, revision 9b3520e501) three thaws took
  350, 180 and 159 ms to RUNNING and 206–384 ms to the lease, on a node
  under shared disk load (vmmd's thaw 3–70 ms, schedd's 34–118 ms). The
  first message arrived as the account's run was finishing the freeze and
  waited for it.
