# Scheduler snapshot-prime handoff observation

Status: accepted

## Context

Image preparation can durably hand off to a release task before any snapshot
exists. Scheduler recovery already rechecks old snapshot_prepare attempts, but
its generic work-pool metrics and logs cannot describe the full durable backlog.
Spec section 12 supports private operator diagnostics. Section 17 and Appendix D
require no change to execution, ownership, frontend or rollout for this slice.

## Decision

Add an optional store observation returning six fixed cohorts: waiting,
release_blocked, active, publication_pending, snapshot_recorded and unknown.
Observe all snapshotting deployments except readiness and intentional workload
holds, excluding deleted apps and foreign owners. Resolve the observer's owner
from the engine's explicit owner, then default-local fallback, preserving empty
app-owner compatibility and unscoped development behavior. Never sum overlapping
replicas. An invalid/missing stage or artifact belongs to unknown, not empty.

Read in one SQLC aggregate or one MemStore lock; return six bounded count/unknown
age/oldest-known-time cohorts. Do not apply recovery's grace, four-page cap or
creation-time candidate filter. Mirror its latest-64-instances-per-app lookback,
warm-first LatestSnapshot selection, stage-attempt boundaries, exact successful
release pins, RAM states, and parked runtime receipt/wake/scope/config freshness.
Reuse environment_runtime_inputs_fresh and environment_runtime_receipt_required;
do not copy secret or variable payloads into observations. Memory uses the existing
locked equivalents. Old snapshots or parked instances before rollback's current
stage cannot prove completion. Stale parked receipts cannot hide waiting work.

Use existing durable stage, release-finish, runtime-change and instance activity
times. The age boundary is no earlier than all applicable known times. A successful
release must have a usable finish time; missing activity evidence preserves
unknown age. No migration or reconstructed historical timestamp is needed.
Age describes the current attempt at its observed classification, not continuous
idle time or how long it has remained in that cohort. A queued or pre-admission
prime may still have no durable instance and therefore classify waiting. A parked
instance is publication-pending evidence, not proof of artifact acceptance or live
traffic. A recorded snapshot also does not mean readiness completed.

Keep classification read-only and independent of recovery's control flow. Add
bounded counters at actual recovery list calls and candidate returns, including
caller cancellation, errors, gates, grace, non-owners, current snapshot, active
instance, fresh parked capture and dispatch_requested. The latter measures the
request only; the existing work pool owns coalescing, inline overflow and actual
execution. Do not count a dispatch request as a completed capture. Preserve order,
batching, return values, token/state fences and all VM lifecycle calls.

Attach the owned collector at Loop.WithOpsMetrics; wiring is idempotent and one
loop owns diagnostics per registry. Start an independent goroutine before Run
subscribes or drains startup work. Read immediately and every 30 seconds, with a
three-second deadline and two-minute freshness ceiling from read start. Cancel
when Run returns. Scrapes use bounded RAM and release locks before emission.
Failed, late-canceled, future, inconsistent or expired evidence omits cohorts;
it must never become zero work. Report read success and frozen last-success
separately. Cancellation/return withdraws expectation without pretending to drain.

Add three recordings for per-class/complete evidence and known attempt age.
Validate six finite integer counts, unknown subsets, coherent times, binary
expectation and initialized recovery vocabularies with job/instance isolation.
Internal warnings: unavailable telemetry held five minutes; currently waiting
known attempt older than five minutes held five minutes; unknown eligibility or
waiting age held 15 minutes; list errors above 10% of non-canceled calls with at
least three errors per rolling five minutes, held five minutes. The waiting
threshold is an initial triage budget, not a measured SLO or execution deadline.
Release-blocked, active, publication-pending and recorded cohorts do not trigger
it. Existing capture failure, work-pool, target and stale-deployment rules remain.

## Validation and adversarial review

Compare memory and real PostgreSQL classifications with recovery's release
fence, snapshots, owner routing and current-attempt rules. Include more than 256
deployments, release success rebasing age, pin mismatch, active/fresh parked work,
runtime invalidation, rollback with old captures and warm-first selection.
Inject malformed/missing/future stage evidence and valid intentional holds in
isolated PostgreSQL. Test actual recovery counters, duplicate/restart/lost-hint
recovery, bounded reads, freshness, nonblocking concurrent scrapes, stopped
expectation and collection while the recovery work pool is full.

Promtool scenarios cover thresholds/holds, aging without arrivals, release/capture
exclusions, unknown history, invalid/missing/future/stale evidence, instance
isolation, query ratios/volume/cancellation, shutdown and recovery. Regenerate
catalogs and preserve every existing definition. Run affected race/database tests,
SQLC, lint and repository policies. This changes observation only; native VM
lifecycle and metal/leak behavior are unchanged.

Adversarial review: do not treat a small batch as the durable backlog; confuse
release execution with eligible waiting; let old rollback evidence hide a new
attempt; infer publication from parking or readiness from a snapshot; ignore
runtime/secret receipts; change recovery ordering while observing outcomes;
mistake process-local scheduling requests for durable progress; or join another
replica's good evidence onto missing telemetry.
