# ADR-1420 · Worker pools scale out in parallel, off the scheduler loop

- **Status:** accepted
- **Date:** 2026-10-17
- **Context:** `TestWorkerScaleFromZeroMetal` on n2 sent one message per
  slot to an idle Pro worker pool (three workers). The workers became ready
  at 11.9, 23.7 and 35.5 s and took their first message at 2.0, 13.9 and
  25.6 s: each started only when the previous one was ready. Two things made
  that so. The pool admitted its missing workers one at a time, and an
  admission returns only when its worker is ready, which for a worker is a
  whole characterization window (`api.CharacterizationDeadline`, 10 s). And
  the targets trigger ran that reconcile inside `Tick`, one arm of schedd's
  single loop `select`, which also runs the reaper, the watchdog, the node
  heartbeat, cron, job dispatch and the notification handler. Every worker
  a pool admitted held all of them for about 12 s.
- **Decision:**
  1. *Parallel admission.* After it stops surplus workers and applies the
     account worker cap, `applyWorkerScopePlan` admits every missing worker
     of the environment at once and waits for all of them. The app's ledger
     cap still applies to each admission. An explicit wake's id
     (`gregale wake` of a worker app) stays with the first worker; the others
     mint their own, because `instances_wake_attempt_active_idx` allows one
     booting instance per wake id.
  2. *Off the loop.* The targets trigger collects the tick's worker apps by
     account and reconciles each account's apps in one background run, in
     tick order. An account has at most one run at a time; a tick that finds
     the account's run still going skips it, and a later tick picks it up. A
     failed reconcile records the app's admission backoff as before. Runs
     use the loop's context, so schedd's shutdown cancels them.
  3. Other callers of the reconcile (a worker app's explicit wake, the
     singleton reconcile, scope overrides) keep their synchronous contract
     and gain only the parallel admission.
- **Consequences:** A pool asked for N workers boots them together: a burst
  is ready in about one boot instead of N, and its first messages are taken
  about 2 s after the send (with ADR-1141's 2026-10-17 amendment). Worker
  boots no longer hold the scheduler loop. Concurrent cold boots share the
  node's CPU and disk, as concurrent request wakes already do. A second
  worker app in the same account can wait up to one boot behind the first
  app's run, and demand that arrives during a run is acted on when the run
  returns; both were already true, and the second now applies only within
  one account.
- **Rejected alternatives:** Booting in the background inside the engine
  (reserve the slot now, boot later) splits the admission path that request
  wakes share, and the next reconcile could not tell an in-flight boot from
  a settled worker without its instance id, which exists only once the
  admission has inserted the row. A background run per app rather than per
  account lets two apps of one account each compute the account's worker cap
  before the other's rows exist, so together they could exceed
  `WorkerReplicasMax`. A separate worker-pool ticker would do the same as the
  background runs with a second cadence to keep in step with the targets
  interval.
- **Validation:** `pkg/sched/targets` tests cover a tick that does not wait
  for a blocked reconcile, one run per account at a time in tick order, and
  another account's run going ahead meanwhile. A `pkg/sched` test boots
  Pro's three workers and requires all three boots to overlap, with the
  explicit wake id on exactly one worker; a store fake those admissions
  share became race-safe. The metal measurement is recorded below.
