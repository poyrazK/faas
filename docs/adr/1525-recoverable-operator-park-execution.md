# ADR-1525: Recoverable operator park execution

Date: 2026-10-17
Status: Accepted; native lifecycle qualification pending

## Context

The force-park dispatcher treated a nil return from the ordinary idle park path
as success. That path can cancel after fresh traffic, and its terminal state
writer only logs database errors. Reclaim also allowed an old worker to finish
a newer claim. Neither an RPC reply nor an absent VM proves a completed park.

## Decision

Use a separate durable protocol for asynchronous `force_park` intents, following
ADR-1522's claim and physical generation fences without invalidating snapshots.

* Schedd prepares an immutable original instance/app/deployment/node/wake,
  migration lineage, physical generation, activity observation and capture plan.
  Preparation checks fresh activity while locking the instance. Fresh traffic
  cancels the intent without touching the VM or entering snapshotting.
* Request/service instances that are running receive one init capture. Optional
  warm capture and cache reuse remain idle-park optimizations. Worker/job instances
  retain signal/grace semantics; unready or snapshot-ineligible instances use a
  generation-bound stop. The result distinguishes a captured park from a stop
  requiring a cold boot.
* Capture preparation atomically enters SNAPSHOTTING, reserves the exclusive
  capture barrier and, for measured runtimes, issues the existing fresh native
  snapshot grant. A failed transaction changes none of these. Existing legal
  state-machine edges suffice; there is no snapshotting-to-running rollback.
* Vmmd commits a unique effect token before the first physical operation. Only
  that transition authorizes execution. It pins the original physical generation
  before quiet resume, snapshot or graceful stop. It never uses unknown-instance
  cleanup. Grant validation, native capture evidence and egress fences remain in
  force. A failed or expired capture can fall back only to a stop of that exact
  generation. Cleanup failure never produces a stop receipt.
* Vmmd persists an immutable result containing confirmed cleanup and optional
  capture/acknowledgment evidence. Receipt retries do not repeat VM effects. The
  token permits late evidence after scheduler claim loss. Started work without
  a receipt becomes `outcome_unknown` and is never automatically redispatched.
* Schedd publishes measured capture acknowledgments through the existing
  admission seam. One claim-fenced transaction then commits the verified PARKED
  or STOPPED state, host-port/barrier release, intent result, completion receipt
  and audit outbox entry. Admission is released only after commit. An uncertain
  attempt with a late receipt is eligible for database-only reconciliation only
  while its original placement, wake and migration lineage still match.

Snapshot row publication stays with imaged (ADR-018). The completion transaction
also enqueues its `snapshot_written` handoff in the existing durable notification
outbox. A failed handoff rolls back completion. Replaying completion cannot
recreate a delivered/pruned handoff. PARKED proves capture, resource cleanup and
publication handoff; imaged retains final cache eligibility checks. Snapshots
remain cache and rootfs remains truth.

Polling exposes phase, physical target, capture/cleanup evidence and final state;
claim, effect, grant and migration credentials remain private. Cancelled,
completed and uncertain results are distinct. Legacy mark-complete setters reject
force-park. The ordinary idle park and legacy unary park interfaces are outside
this protocol and must not be used to recover an operator attempt.

## Rollout and validation

Quiesce operator dispatch, apply the migration, upgrade vmmd, then replace all
schedulers together. Mixed scheduler writers are unsupported. Migration preserves
legacy running parks as `outcome_unknown`, without inventing execution evidence.
Downgrade refuses to discard journal rows or unresolved legacy parks.

Portable memory/PostgreSQL, scheduler, RPC and manager tests cover fresh traffic,
claim loss, duplicate effects, lost replies, crashes, failed commits, cleanup
failures, replacement generations, late receipts and migration quarantine. Native
capture, process/cgroup/network teardown and resource leak qualification require
the supported native x86_64 KVM hosts, `make test-metal` and `make leakcheck`.
