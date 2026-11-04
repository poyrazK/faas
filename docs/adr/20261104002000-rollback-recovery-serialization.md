# ADR-20261104002000 · Serialize rollback recovery

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** [ADR-1451](1451-guarded-reversible-config-changes.md),
  [ADR-1446](1446-safe-releases-by-default.md),
  [ADR-625](625-first-wake-5xx-rollback-from-request-telemetry.md),
  [ADR-091](091-edge-rules.md),
  [ADR-601](601-binding-checked-historical-rollback.md),
  [ADR-845](845-route-monitor-automatic-rollback.md), and
  [database-restoring rollbacks](20261023181931-rollbacks-that-restore-the-database.md).
- **Decision:** Bind automatic rollback preparation to the deployment whose
  health was judged, commit configuration restoration atomically, rotate
  through pending config guards, and retain edge and binding checks through
  rollback writes and compensation. Reconcile ambiguous database-switch code
  changes, restore project traffic conditionally, recheck delayed release
  controls, and commit monitor intent with its incident link.
- **Why:** A newer healthy release could replace the observed failing release
  before rollback preparation. Individual config writes could fail halfway
  through restoration; the guard then mistook its own partial restore for an
  outside edit. Fifty quiet guards monopolized the worker's oldest-first
  batch. Edge rollback computed affected hosts before taking its mutation
  lock. Database switches both failed open on lookup errors and inherited a
  request-level binding-policy refusal that had no compatible switch request.
  A follow-up review reproduced database-switch compensation that trusted a
  missing timestamp after a lost commit reply, project rollback that either
  overwrote a newer release or left a retained baseline at zero traffic,
  checked rollback that routed a never-served target through a new freeze,
  and monitor claims stranded before operation creation.

## Recovery boundaries

Automatic first-wake and post-release rollback carry an internal expected
current deployment into `PrepareDeploymentRollback`. PostgreSQL checks that
deployment under the existing app traffic lock; MemStore checks under its
mutex. A changed deployment refuses preparation. The existing preparation
marker still withdraws rollback if a new release cuts over during readiness.
Public manual and checked rollback request semantics are unchanged.

`ApplyConfigRestore` compares the observed variables, secret fingerprints and
guard head while holding the mutation boundary. It commits variable writes,
retained secret restoration/deletion, existing secret lifecycle journals,
revision recording and snapshot invalidation together. Automatic restoration
also closes the exact guard in that transaction. PostgreSQL publishes advisory
cache invalidations on commit and commits its restart claim and durable restart
notification with the restore. An error leaves configuration and the guard
available for another attempt. Secret rotation/deletion remains unrestorable;
manual revision rollback still reports secret differences without restoring them.

Each config worker keeps a keyset cursor over `(last_change_at, id)`, advances
after evaluating a guard, and wraps at the end. No schema migration or new
public cursor is needed. A restarted worker begins at the oldest guard and
continues forward; transient failures cannot monopolize a batch.

Edge rollback takes the existing account/app mutation lock before reading the
target and current rule sets, validating them, and calculating the host union.
The prepare, restore and apply phases retain that lock.

Database-restoring rollback may be admitted with an enforced binding policy.
Its worker evaluates the published binding inventory and supplies the existing
binding fences around each code traffic write, including compensation. Each
promotion also requires the prepared rollback marker inside the traffic write;
a newer release that withdraws the marker cannot be overwritten. The
worker can evaluate a prepared, zero-traffic target only after reading its
rollback marker and confirming it has no resident or starting instances. Public
probe and promotion selectors continue to require a live deployment. An additive
function migration lets the existing database fence authorize that prepared
target without changing its status before the guarded traffic transaction;
revision, expiry, artifact and policy checks remain mandatory. The previous
release continues to work with the expanded function, and Down restores the
live-only predicate.

The cutover independently requires fresh native verification for every replacement
database credential before publishing it. Binding policy is not disabled or
downgraded. An unavailable release guard retries while the app remains fenced;
a definite refusal aborts before the target is made live.

After acquiring its app fence, a database switch reconciles the actual serving
deployment during compensation even if `release_switched_at` was not saved.
A promotion may have committed before its acknowledgement was lost. The worker
restores the source code before cancelling the cutover and releasing its fence;
an activated cutover still wins and restores the target instead. Operations
that never acquired the fence do not compensate unrelated code changes.

Non-graph project rollback uses `RestoreDeploymentTrafficIfCurrent` instead of
marking an already-live retained revision live again. Under the normal app
traffic lock, the store checks the exact serving deployment and resets the
retained candidate's rollout fields inside the existing promotion transaction.
The existing artifact, capacity, lifecycle, MCP and traffic checks then run
before all traffic moves to the baseline. An error leaves its original status
and weights intact; a newer serving release is a conflict. No separate
prepared marker escapes the transaction. Binding-enforced scopes continue to
require the checked historical workflow.

Checked rollback rechecks current release controls after readiness and binding
evaluation, immediately before routing. Durable serving history distinguishes
a known-good recovery from a release that never served: a new freeze holds the
latter, while the former remains available during an incident. A temporary
refusal keeps the operation blocked and retryable.

A route-monitor `claimed` decision without an operation is resumable. The
internal worker attaches its saved claim to checked-intent creation. MemStore
holds its mutex; PostgreSQL locks the app and monitor before deployments. In
that boundary the store verifies the incident and exact deployment pair,
creates one checked operation and readiness outbox entry, and records its ID
on the incident. Concurrent retries return the same operation. A late write
failure rolls everything back; a lost acknowledgement leaves a complete link
for the next worker. Transient API lookups keep the claim available, while a
definite refusal still records a single skipped decision. This uses existing
rows and JSON fields and adds no public protocol or schema requirements.

## Validation

Shared store conformance cases exercise atomic failure after earlier writes,
concurrent-edit refusal, cursor pagination, and expected-current rollback on
MemStore and PostgreSQL. HTTP/worker regression tests cover stale health
observations, later failing guards, transient restore retries, binding-policy
admission and enforcement, lookup-error retries and checked compensation.
An edge concurrency test holds the mutation lock while another editor adds a
host and requires both fleet phases to cover it.

Additional regressions exercise lost promotion acknowledgements, concurrent
project releases, retained baselines on both stores, freeze hold/resume and
known-good recovery, monitor claim retries, and atomic incident linking.
PostgreSQL fault injection rejects the incident write after intent creation;
the operation and target preparation must both roll back. Concurrent retries
must create and link exactly one operation.

These checks do not replace the existing native VM and provider qualification
requirements for a deployed rollback or database switch.
