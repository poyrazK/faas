# ADR-20261018101500 · Worker pools scale out off the scheduler loop, with brief characterization for proven workers

- **Status:** accepted
- **Date:** 2026-10-18
- **Context:** `TestWorkerScaleFromZeroMetal` on n2 sent one message per
  slot to an idle Pro worker pool (three workers). The workers became ready
  at 11.9, 23.7 and 35.5 s and took their first message at 2.0, 13.9 and
  25.6 s. Each worker cold boot spends about 11 s in readiness, nearly all of
  it guest-init's characterization window (`api.CharacterizationDeadline`,
  10 s): a process that never listens is called a worker only once the
  window has passed. The pool admits one worker at a time and an admission
  returns only at readiness, so N workers took N windows. The targets
  trigger ran that reconcile inside `Tick`, one arm of schedd's single loop
  `select`, which also runs the reaper, the watchdog, the node heartbeat,
  cron, job dispatch and the notification handler; each admitted worker held
  all of them for about 12 s.
- **Decision:**
  1. *Off the loop.* The targets trigger collects the tick's worker apps by
     account and reconciles each account's apps in one background run, in
     tick order. An account has at most one run at a time; a tick that finds
     the account's run still going skips it and a later tick picks it up, so
     each reconcile computes the account's worker cap while no other run
     changes the account's workers. A failed reconcile records the app's
     admission backoff as before. Runs use the loop's context. Other callers
     of the reconcile (an explicit wake, the singleton reconcile, scope
     overrides) are unchanged.
  2. *Brief characterization for proven workers.* vmmd remembers each
     deployment whose declared worker (`execution_mode: worker`) passed a
     cold boot's full characterization as a worker on that node. A later
     cold boot of that deployment adds `faas.worker=proven`
     (`api.ProvenWorkerBootArg`) to the kernel command line, and guest-init
     then watches for `api.ProvenWorkerCharacterizationWindow` (1 s) instead
     of 10 s: the image has already shown it starts and stays up without a
     listener, so the boot only has to show that its process started. A
     failed cold boot forgets the deployment, so the next one runs the full
     window and a regression is reported as a startup failure. The set lives
     in memory; a vmmd restart costs one full window per deployment. vmmd
     reads the deployment from the boot RPC's correlation metadata, which
     the deploy prime did not carry, so schedd's cold-boot call now names
     the app, deployment and instance when its caller has no wake
     correlation; the prime's boot then proves its deployment.
     Requests, services, jobs and every deployment's first boot on a node
     keep the full window.
  3. *A queue message wakes an idle pool.* schedd already listens on
     `invocation_due`, which fires when a message is inserted or returns to
     pending. For a queue message it starts that app's pool run at once
     instead of at the next tick, when the last tick saw the app as a
     queue-driven worker pool, the app is outside admission backoff and the
     node ledger counts no serving instance of it (a frozen worker holds
     none). It is the tick's per-account run. A wake that finds the
     account's run going is queued for that run, which checks it again
     when it finishes and reconciles the app if it still qualifies: on n2
     a message sent while that run was freezing the pool's only worker
     (the row is FROZEN before the ledger releases the worker) otherwise
     waited for the next tick. The tick still covers a dropped
     notification. A pool with a serving worker leaves the message to that
     worker's lease and the tick, so a busy queue adds no reconciles.
- **Consequences:** A pool's later workers are ready about 1 s after their
  process starts instead of 10 s, and the pool admits the next worker that
  much sooner; with ADR-1141's 2026-10-17 amendment each still takes its
  first message about 2 s after it is admitted. Worker boots no longer hold
  the scheduler loop. A proven worker that crashes after its first second is
  handled like any crash after readiness, by the restart policy and the
  liveness path, not as a startup failure; the deployment's first boot still
  catches startup crashes in the full window. A worker that starts listening
  more than a second after start passes as a worker, where the full window
  would have rejected it as `http`; workers never join the gateway target
  set either way.
  An idle pool's first worker, a thaw (ADR-20261019103000) or a cold boot,
  starts up to one tick (1 s) sooner. The ledger count is per app, so an
  app with a serving worker in one environment and an idle pool in another
  still waits for the tick there.
- **Rejected alternatives:** *Admitting a pool's missing workers in
  parallel.* Tried on n2 (branch `worker-parallel-admission-wip`): three
  workers were admitted within 0.4 s, but three concurrent cold boots on two
  vCPUs started each process about 3.5 times later (11 s), past the host's
  15 s characterization deadline (`api.CharacterizationHostDeadline`,
  counted from VM start). All three boots failed and the pool could not
  reach three workers in three minutes. *Dropping the window for every
  declared worker* would let a deployment whose worker crashes after a
  second go live and crash-loop instead of failing its deploy. *Telling the
  guest through schedd and the wake RPC* needs a proto field for a fact
  vmmd already observes. *Lengthening the host deadline* would let parallel
  boots finish but leaves the ten-second wait in every boot.
- **Validation:** `pkg/sched/targets` tests cover a tick that does not wait
  for a blocked reconcile, one run per account at a time in tick order, and
  another account's run going ahead meanwhile; a queue wake that reconciles
  only an idle queue-driven pool the last tick saw, honors admission
  backoff, and during the account's run waits for it and is checked again
  when it ends; and, in `cmd/schedd`, which
  notifications are queue messages. `pkg/fcvm` tests cover the
  boot argument (declared workers only) and which boots prove or forget a
  deployment; `guest/init` tests cover the short window and the kernel
  argument parsing; a `pkg/sched` test covers the cold-boot RPC's identity
  without wake correlation. The metal test requires every pool boot after
  the deployment's prime to spend less than the full window in vmmd's
  `wait_ready` phase. It passed on n2 (2026-10-18): every pool boot, the
  first one after the deploy included, waited 2.1–3.0 s in `wait_ready`
  (about 11 s before); one message was RUNNING at a median 4.9 s (11.9 s
  before) and leased at 4.0 s; a burst of three was RUNNING at 5.0, 8.5 and
  11.9 s (11.9, 23.7 and 35.5 s before) and leased at 4.1, 7.6 and 11.0 s.
  The node's disk was under heavy shared load in that run, which lengthened
  disk staging (`provision`, 0.6–2.4 s) and the token times.
