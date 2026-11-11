# ADR-191 · Scheduler divergence reconciliation and bounded loop dispatch

- **Status:** accepted
- **Date:** 2026-09-21
- **Decision:** (1) Reconcile `instances` rows against what the owning vmmd
  actually reports, shipped report-only behind `FAAS_SCHEDD_RECONCILE_ENFORCE`.
  (2) Move every long-running notification handler onto one bounded, coalescing
  work pool, and cut schedd's main-loop stall budget from 180 s to 60 s.
- **Why:** ADR-190 made a wedged daemon restart itself, but both scheduler-side
  causes behind it remain. Rows and reality drift apart with no owner for the
  "row says RUNNING, VM is gone" direction, and the notification loop still
  shares one goroutine with eighteen tickers.
- **Consequences:** new metrics `schedd_instance_divergence_total{outcome}`,
  `schedd_loop_work_total{kind,outcome}`,
  `schedd_loop_work_duration_seconds{kind}`; new alert `FaasInstanceDivergence`
  (warn) with runbook; new env `FAAS_SCHEDD_RECONCILE_ENFORCE`; schedd
  `WatchdogSec` 180s → 120s. No migrations, no proto changes.
- **Rejected alternatives:** listed per decision.

## Note on the ADR number

`docs/adr/190-production-buildkit-cache.md` and
`docs/adr/190-daemon-durability-primitives.md` both exist: two PRs picked 190
concurrently and the BuildKit one merged first. The repository already carries
several duplicate numbers (157, 158, 167, 168), so this ADR takes 191 rather
than renaming 67 references across the tree for a cosmetic fix. Worth a
dedicated cleanup PR that renumbers and adds a CI gate, not worth doing here.

## 1. Instance divergence reconciliation

### Context

`instances` rows and running Firecracker processes drift. vmmd owns one
direction: `fcvm.ReapOrphanedJails` tears down VMs with no live row on vmmd
boot. A production node on 2026-09-04 had 23 such VMs, the oldest 3.7 days,
5.3 GB of tenant RAM.

The other direction had no owner. A row that says RUNNING while the VM is gone
keeps billing the customer at plan RAM + 8 MB per second (§4.7), keeps its
admission slot reserved against the 47,600 MB ceiling, and keeps receiving
routed requests that fail. `Engine.ReconcileDeadNodeInstances` covers only a
whole node going silent (`compute_nodes.last_heartbeat_at` older than
`DeadNodeReconcilerStalenessSeconds`); a single VM dying under a healthy node —
an OOM kill outside the liveness path, an operator's `kill -9`, a destroy RPC
that failed after the VM died — is invisible to it.

### Decision

`InstanceDivergenceReconciler` (`pkg/sched/instance_divergence.go`) sweeps every
`InstanceDivergenceIntervalSeconds` (30 s) for live rows the owning vmmd is not
reporting.

**The signal already exists.** vmmd streams per-instance capacity telemetry to
schedd continuously; it lands in `NodeTelemetryCache` and the instance-stats
poller projects it into the reader behind `InstanceActivityReader`, which
already drops stale samples ("Stale samples are absent, not zero"). A key
present in `SnapshotActivity` means the owning vmmd reported that VM recently.
The reconciler consumes what is already flowing: no new RPC, no proto change,
no second `Stats` call per node.

**Four gates before a row counts**, each closing a specific false-positive path:

| Gate | Closes |
|---|---|
| The node must have reported ≥1 of its own instances | A silent node is the dead-node reconciler's job; the two sweeps take disjoint inputs and cannot act on one row |
| Instance older than `InstanceDivergenceGraceSeconds` (60 s) | A just-admitted VM missing from the last telemetry batch |
| Absent on two consecutive sweeps | One dropped telemetry batch parking a live app |
| An empty snapshot resets all candidates | A telemetry outage accumulating candidates that all fire when it returns |

Plus `InstanceDivergenceTickLimit` (50) on the per-tick write burst, the same
bound and reasoning as `DeadNodeReconcilerTickLimit`.

**Report-only by default.** `FAAS_SCHEDD_RECONCILE_ENFORCE=1` is required before
any row is written; otherwise the sweep increments
`schedd_instance_divergence_total{outcome="suppressed"}` and logs what it would
have repaired. A bug in this sweep parks healthy apps fleet-wide, so the
divergence counter gets read against production reality for a week before
enforcement is flipped in a one-line follow-up.

**FAILED, not PARKED**, under enforcement, for the reason
`ReconcileDeadNodeInstances` already records: no snapshot was taken because the
VM is gone, and claiming PARKED asserts a snapshot that does not exist. FAILED
is cold-bootable (ADR-005: snapshots are cache, not truth), so the customer's
next request still serves — it just pays the cold-boot path. The transition
goes through `UpdateInstanceStateIf` so a peer that already parked, evicted or
migrated the row wins, and `Ledger().Release` runs on the conflict path too
because it is idempotent and closes the billing side of the same race.

### Known gap

A node whose *only* instance dies reports nothing, which is indistinguishable
from a silent node, so it is skipped. That row stays live until the node's own
heartbeat fails or the next wake reconciles it. Closing this needs a vmmd-side
"I am up and I have zero VMs" assertion — a separate change, deliberately not
bundled here.

### Rejected alternatives

- **A `ListInstances` RPC on vmmd.** The telemetry stream already carries the
  set; a second source would drift from the first and add a proto change plus a
  per-node call every 30 s.
- **Acting on the first absent sweep.** Telemetry is best-effort and batched.
  One dropped batch would park live customer apps, which is a worse outcome than
  30 s of extra billing on a dead VM.
- **Destroying VMs with no row from schedd** (the reverse direction). Schedd
  would be ordering a kill from a possibly-stale database read; the failure mode
  is killing a customer's running app. That direction stays in vmmd, where the
  durable gate already lives.
- **Enforcing from day one.** Closes the billing leak a week earlier at the cost
  of a fleet-wide false-positive risk with no production evidence behind it.

## 2. Bounded loop dispatch

### Context

`Loop.Run`'s select goroutine owns the pg_notify channel and eighteen tickers.
Anything a handler arm does synchronously delays the reaper, the §6.1 watchdog,
cron and heartbeat. On 2026-09-03 a deadline-less `PauseAndSnapshot` held that
goroutine for 10+ minutes; schedd stayed `active`, answered `/metrics` in 30 ms,
and did no work at all.

`dispatchPrime` escaped with a bounded four-slot pool. Four other arms escaped
with a bare `go func`, which removes the head-of-line blocking but replaces it
with unbounded goroutine growth: a burst of `app_changed` spawns one goroutine
per notification, each taking the same per-app engine lock, against a
16-connection Postgres pool.

### Decision

One `workPool` (`pkg/sched/loopwork.go`) for all five kinds, with per-kind slot
budgets and a per-kind coalescing key that generalises the prime single-flight.

The overflow policy is per kind because the right answer differs:

- **`prime` overflows to inline.** A dropped `snapshot_prime` strands the
  deployment in `snapshotting` with nothing to retry it — the notification is
  consumed and gone. Blocking the loop stays the lesser harm, bounded by
  `SnapshotTimeout`. Behaviour is unchanged from before this ADR.
- **The four reconcile kinds overflow to drop.** Each is idempotent over a
  durable table with a safety ticker behind it, so a dropped duplicate costs one
  tick of latency. Dropping beats unbounded goroutines.

A panicking task is recovered, counted and its slot and key released: the loop
is the only thing keeping this node scheduling.

`handleAppWake` and the two private-network arms stay **synchronous on purpose**.
Each returns an error that decides whether the durable notification is
acknowledged; moving them off-loop means re-plumbing outbox ack through the
worker, which is a separate change.

`MainLoopBudget` drops 180 s → 60 s, and schedd's `WatchdogSec` 180 s → 120 s to
keep the ADR-190 invariant that the watchdog outlasts twice the loop budget. It
cannot go below 60 s while `handleAppWake` is inline: `EnsureWake` can
legitimately cold-boot at `ColdBootTimeout` (35 s) plus admission.

### Rejected alternatives

- **One global worker pool.** A burst of cheap app reconciles would starve the
  prime path, which is the one kind that cannot be dropped.
- **Unbounded `go func` everywhere** (the status quo for four arms). Bounded
  latency, unbounded resource use; the connection pool is the real limit and
  nothing was respecting it.
- **Moving every arm off-loop, including the durable ones.** Needs outbox ack
  plumbed through the worker. Worth doing, not worth coupling to this change.

## Amendment (2026-10-05): the reaper tick runs on the work pool

The idle reaper still ran on the select goroutine. It parks, stops, evicts and
reconciles lifecycle and warm pools sequentially, and a park that captures a
snapshot is not short: across 405 production-us parks in 24 h, p50 was 0.16 s,
p90 22.75 s and max 35 s. One tick therefore held every wake notification,
watchdog sweep and heartbeat on the node behind its parks. It could also
exceed `MainLoopBudget` on its own.

The tick is now the `reaper` work kind: one slot, overflow drop, key `tick`.
It is still one sequential task, so park order, per-app caps and billing
timing are unchanged. A tick that fires while the previous one runs
coalesces into it, and the next tick re-reads the instance table. The
reaper's Loop-held state (`lastFloorByApp`, `runningReasonStates`) is touched
only by that task. Engine methods already serve concurrent work-pool callers
(prime, reconciles) and take the per-app engine lock, so a wake for the same
app still waits for that app's park, while wakes for every other app no
longer do. Test: `pkg/sched/loop_reaper_dispatch_test.go`.

## Amendment (2026-10-12): the other direction while vmmd is up

Decision 1 left the "VM runs, row is not live" direction to vmmd's boot reap.
On production-us that was not enough. For 36 minutes a starved control plane
timed out one heartbeat ping after another, and every timeout marked a node
unavailable. Each node.failed sent the recovery arbiter's Recreate verdict to
`RecreateInstance`, which parks the rows as if their VMs had died with the
node. The VMs had not died: the nodes were healthy. Each later wake booted a
new VM that the next flap orphaned in turn. After about 22 flaps per node, 53 VMs
(5.5 GB on two 16 GB nodes) were running behind parked rows. Admission could
not see them, and nothing reclaimed them until an operator did (production-us
hunt #9, H9-171).

Three changes, each covering a different failure:

1. **The trigger.** The heartbeat marks a node unavailable only after
   `DefaultHeartbeatFailureThreshold` (3) consecutive failed pings. At the
   30 s interval that is within the 90 s staleness budget the stale-peer
   observer already uses, so a dead node is still failed over in the same
   time.
2. **The arbiter's own leftovers.** `RecreateInstance` records each instance
   it parks. When the node answers a ping again, schedd destroys those VMs
   through vmmd, unless the row is live again. Retries last up to an hour.
3. **Every other cause.** vmmd sweeps its ordinary app VMs once a minute
   (builder, execution, app-task, qualification, job and warm-pool VMs are
   excluded). It destroys a VM whose instance row exists and is not live on
   two consecutive sweeps. A missing row is not evidence, so those VMs stay
   with the boot reap. The two-sweep rule keeps it from racing a park whose
   own destroy is still in flight. Unlike decision 1 this is enforcing from
   the start: a non-live row (parked, stopped, failed, evicting) never owns a
   running VM, and the live set (`state.IsLive`) includes draining,
   snapshotting, migrating and warm.

Tests: `TestHeartbeat_FailureThreshold`,
`TestLostVMsAreDestroyedWhenTheNodeAnswers`, `TestLostVMDestroyFailureIsRetried`,
`TestLiveVMReapDestroysVMsWhoseRowIsNotLive`, `TestLiveVMReapRetriesAFailedDestroy`,
`TestManagerAppInstanceIDsListsOnlyOrdinaryAppVMs`.

## Amendment (2026-10-25): event fanout reruns after a coalesced wake

Event fanout is the `event_fanout` work kind: one slot, overflow drop, key
`tick`, woken by `event_published` and a 5 s safety ticker. A wake that
arrived while a sweep ran coalesced into it and was dropped, but the sweep
had often read past the new event already: it prunes, runs recoveries and
backfills before it claims events. The event then waited for the safety
tick: in the V12 pipeline runs, one event per run took about 5 s from
publish to handler.

A kind may now set `rerunCoalesced`. A submit that coalesces into a running
task of such a kind records itself, and the task runs one more pass when its
current one ends, however many wakes arrived meanwhile. Only `event_fanout`
sets it. Each extra pass counts as outcome `rerun` in
`schedd_loop_work_total`.

Ending a task also drops its key and frees its slot in one step under the
pool lock. Before, the key went first and the slot after, so a submit landing
between them found no task to coalesce into and no free slot, and was dropped
as saturated. That holds for every kind.

Tests: `TestWorkPoolRerunsEventFanoutAfterCoalescedWake`,
`TestWorkPoolDoesNotRerunOtherKinds`, `TestWorkPoolEventFanoutNeverStrandsAWake`.

## Amendment (2026-10-26): explicit wakes leave the select goroutine

Decision 2 left one VM-booting arm on `Loop.Run`'s select goroutine: the
durable `app_wake` handoff (an API or webhook wake), together with
`runtime_config_restart`. Each could hold the goroutine for a whole wake
(the cold-boot budget plus admission). Meanwhile the reaper, cron, the
watchdog and every other notification on that schedd waited, and N explicit
wakes ran one after another. They stayed inline only because their error
decided whether the outbox row was acknowledged.

Both now run on the work pool (`app_wake`: 8 slots, `runtime_config_restart`:
4, both `overflowDrop`, coalesced per outbox row) and settle their own row:

1. **Owner first.** Every schedd hears the broadcast. A schedd that does not
   own the app returns before it touches the row, as the inline arm did.
   Claiming first would let a peer fail the row into retry backoff while
   the owner waited.
2. **Fenced delivery.** The owner delivers through
   `db.DeliverNotificationForNode`, the renewable claim imaged already uses
   (ADR-464). The claim is renewed for the length of the boot, so the replay
   worker no longer delivers the same row a second time once the 5 s wakeup
   grace passes, which the inline arm allowed. Success completes the row,
   a failure enters the outbox's bounded retry, and a deferred
   runtime-config refresh is deferred. Ownership that moves between the
   check and the handler releases the row untouched (`ErrNotificationNotOwned`).
3. **Overflow.** A delivery the pool cannot take stays pending, and the
   replay worker delivers it after the grace period (≤ about 7 s later).

Unchanged: the private-network arms.

**Replay of another node's wake (follow-up, 2026-10-26).** The rows name no
node, so every schedd's replay worker claims any of them. A peer used to fail
another owner's row: that spent one of its bounded attempts and moved the
owner's next try onto the retry backoff, and enough peers could dead-letter a
wake the owner never saw. Releasing it as `ErrNotificationNotOwned` would
stop the peer's drain pass behind the foreign row. schedd's replay handler
now turns `ErrAppOwnedByAnotherNode` into a deferral of one retry interval
(5 s). That gives the attempt back and leaves the row pending for the owner,
and the peer's drain moves on. Replay matters only when the owner missed the
broadcast, since the owner's immediate delivery claims the row first.

Tests: `TestExplicitAppWakeLeavesTheLoopGoroutine`,
`TestExplicitAppWakeLeavesAnotherNodesApp`,
`TestExplicitAppWakeSettlesItsOutboxRow`,
`TestDurableReplayDefersAnotherNodesWake`.

## Amendment (2026-11-05): bounded recovery scans retain progress

Snapshot-prime recovery retains its candidate offset between ticks while
keeping its four-page, 256-row work budget. The stale-deployment reconciler
likewise retains its offset across 64-row sweeps. Both advance past skipped
rows and wrap after a short or empty page, revisiting rows whose eligibility
changed or whose position shifted after concurrent removals. The offsets are
process-local progress hints; a restart begins a new pass. Candidate eligibility,
ownership, release-task fences and cancellation guards remain authoritative.
This prevents persistent approval holds or queued builds at the front of the
candidate list from starving unrelated deployment recovery.

### Delayed prime work and attempt ownership (2026-11-05)

A retained deployment artifact is not authority to start a superseded worker.
Snapshot priming checks the current lifecycle before admission, rechecks its
artifact and stage-start identity before VM boot, and fences runtime publication
against that same attempt under the store's ownership locks. A deployment that
is superseded during boot cannot publish its provisional instance as RUNNING;
the scheduler destroys it and releases the reservation. Re-priming a historical
rollback resets its stage clock, so stale work for the previous attempt cannot
publish into or fail the replacement attempt. Existing active legacy prime
callers retain their lifecycle contract. Durable notification replay remains a
hint and never changes these checks.

### Prime replay and failure ownership (2026-11-05)

A LIVE deployment whose readiness stage is complete does not authorize another
boot when a prime hint is replayed. An explicit rollback resets snapshot_prepare
and its clock, authorizing a fresh attempt. Active legacy prime callers retain
their existing stage contract.

Prime errors retain the attempt that executed them, including typed startup
problems and retry classification. Before persisting a failure, schedd verifies
that the current row still belongs to that attempt. The store repeats that check
under its write lock and commits failure status and stage finalization together.
A requeued attempt therefore cannot inherit the previous boot's failure. A
watchdog diagnostic can still be refined when its failed stage history proves
it belongs to that same attempt.

A RUNNING worker whose runtime proof is stale cannot suppress prime recovery
while its deployment is still snapshotting. Recovery checks the current
attempt, configuration, app owner and instance identity under the store locks,
then withdraws only that stale worker as DRAINING before destroying it. This
prevents readiness promotion racing the cleanup. Capacity remains accounted
until teardown commits; interrupted teardown resumes on a later recovery pass.
Only then may the existing prime dispatcher admit fresh capacity. Fresh workers
and in-flight boots retain their existing protection against duplicate primes.

Prime allocation is also serialized across scheduler processes by a separate
per-deployment lock, independent of imaged's activation lock. Replaying a hint
for the same preparation resumes its existing outcome instead of executing
customer code again. A completed capture retains its private activation message,
original runtime fence and wake identity in the same transaction that parks the
instance. An ephemeral-secret prime retains its no-snapshot readiness message
with its STOPPED transition instead. Worker readiness can be reconstructed from
the existing RUNNING instance's current runtime proof.

Notification enqueue failure leaves these handoffs recoverable, rather than
failing a successful deployment. Periodic recovery and duplicate hints replay
the retained message without booting another VM; a lost completion acknowledgment
also releases the stale local capacity reservation. Requeued preparations,
changed artifacts, replaced wakes and changed runtime inputs cannot reuse this
evidence. Legacy parked captures without a retained handoff may be primed again
when recovery cannot find their publication. Imaged remains the sole owner of
snapshot publication and traffic activation.


### RUNNING prime capture recovery and session capacity (2026-11-06)

A scheduler can die after runtime publication commits RUNNING but before the
request/service prime starts capture. A healthy VM is neither a watchdog
transient nor a divergence candidate, and service mode has no idle reaper.
Periodic recovery therefore dispatches RUNNING primes as well as completed
captures. Under the app and deployment-prime locks, Prime resumes capture on
the original VM, retaining its preparation, runtime proof and wake identity.
A capture barrier left by the crashed prime is cleared before reacquisition;
active exclusive-operation leases still prevent capture.
The ordinary park path retires changed runtime inputs or incompatible RAM
before capture. Unavailable inputs leave the VM and its capacity intact;
busy exclusive operations and uncertain handoffs remain recoverable. Capture
errors retain the normal prime failure classification. Workers retain their
existing readiness reconstruction and guarded stale-worker retirement.

In pooled fleets, schedd's direct session pool reserves seven connections:
one notification hub, four background prime locks, one inline overflow prime
lock and one short session operation. Worker concurrency and pool sizing share
`api.SnapshotPrimeConcurrentSlots`; `api.SnapshotPrimeInlineSlots` records the
existing single inline dispatch slot. Per-compute direct capacity rises from
11 to 16 in the PostgreSQL capacity role, including imaged's existing extra
session. Converge that capacity plan before upgrading schedd. Ordinary query
pool sizes, deployment ownership locks and the dispatcher overflow policy are
unchanged.


### Readiness evidence and activation-failure ownership (2026-11-06)

Ephemeral-secret readiness retains the stopped request/service prime as its
recipient. Promotion rechecks that recipient's wake, node, start time and runtime
configuration proof under the existing app/deployment/instance locks. A stopped
instance from an older preparation cannot satisfy a requeued rollback, even if
its configuration is unchanged. Completed LIVE activation remains idempotent.

All active snapshot and no-snapshot activation paths bind their preparation and
runtime inputs before verification. Both generic failure writers honor that
context in the final failure transaction, locking environment, app and deployment
before any stage, error, traffic or outcome writes. A contract, source-reference
or route-removal verdict from a timed-out preparation cannot fail its replacement.
Failure writes also recheck the readiness recipient and its runtime proof when
present, so an obsolete readiness hint cannot authorize an activation failure.
Obsolete failure verdicts are acknowledged without emitting a failed state for
the new preparation. Existing failure callers without activation context retain
their legacy contract; scheduler prime failures keep their separate attempt guard.
No schema or notification-payload change is required.
