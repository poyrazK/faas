# Durable operator image-preparation phase progress

Status: accepted

## Context

The build-to-image handoff observer excludes checkpointed work by design.
Process-local commit counters distinguish claims from forward transitions, but
cannot establish durable age after restart. Checkpoint updated_at changes on
every claim, including retries and busy leases, so cannot measure phase progress.
Spec section 12 supports this observation; section 17 and Appendix D add no
execution or ownership requirement.

## Decision

Add a nullable private phase_started_at column with a finite, positive timestamp
check. Do not backfill from updated_at, build finish or deployment creation: none
establishes the current phase's start. A row trigger stamps the database clock on
insert and phase change, preserving OLD.phase_started_at on same-phase updates.
This covers older writers during mixed-version operation, including INSERT ON
CONFLICT claims. Reclaim cannot manufacture legacy history. Timestamp and phase
commit or roll back together under existing fences. Mirror insertion/advancement
timing in MemStore. The trigger observes changes; it does not authorize them.

Add an optional consistent store snapshot of all active/resumable checkpoints,
grouped into preparing, layer_published and scan_complete. Exclude handed_off,
live and terminal work. Match recovery's node/empty-owner predicate; never sum
overlapping scopes. Use one SQLC aggregate or one MemStore lock, without recovery's
16-row limit or activation lock. Return pending, unknown-age count and oldest
KNOWN start per phase. Reject invalid timestamps even alongside a valid minimum.
Do not change phases, scheduling, ordering, batching, ownership or artifacts.

Start an independent observer before synchronous recovery: read immediately and
every 30 seconds, with a three-second deadline and two-minute freshness ceiling
measured from read start. Cancel when Loop.Run returns. Scrapes copy bounded RAM
and release the mutex before sending. Publish all phases only after a valid,
successful, fresh read. Failed, late-canceled, future, inconsistent or expired
evidence is unknown, never an empty backlog. Keep read success and frozen
last-success separate. Cancellation/return withdraws expectation without drain.

The known-only timestamp is zero when no known ages exist. Unknown-only phases
have no recorded age; proven empty phases record zero age. Mixed cohorts retain
known ages and separately expose unknown history. Use fixed phase labels only,
without customer, deployment, owner, artifact or error labels. Direct OCI image
preparations are included alongside succeeded source builds.

Three recordings validate coherent phase evidence, complete three-phase evidence
and oldest known age. Require target up, recent successful collection, finite
integer counts, consistent unknown counts/timestamps and binary expectation.
Match job/instance/phase, preventing replicas from lending missing evidence.
Add three internal warnings:

- Telemetry unavailable for five minutes unless explicitly disabled.
- Known phase age above 30 minutes for preparing or layer_published, or 10 minutes
  for scan_complete, held five minutes with fresh enabled observation.
- Unknown historical phase ages persist for 15 minutes in fresh enabled snapshots.

Thresholds are initial operator triage budgets, not measured SLOs or execution
deadlines. Preparing includes input acquisition, assembly and leases;
layer_published includes replication, base staging and scanning; scan_complete
includes final handoff. Ages also include waiting to resume behind serial work.
A healthy long operation can warn: age does not prove a deadlock, failed image,
missed two-second tick or readiness failure. Handed_off may transfer ownership to
a release task and does not mean live. Existing rules remain unchanged.

## Validation and adversarial review

Exercise memory and real PostgreSQL insertion, repeated reclaim, foreign owners,
stale tokens, transitions, cancellation, handed-off exclusion and more than one
batch. Verify timestamp/phase rollback, nullable legacy rows, mixed-version
inserts/updates, mixed cohorts and migration rollback. Reuse crash/restart and
real busy-artifact lease tests. Race-test bounded reads, nonblocking scrapes,
shutdown, observation during blocked execution and exact freshness boundaries.

Promtool covers exact thresholds/holds, continued aging without arrivals,
repeated claims, advancement, empty/unknown/mixed cohorts, instance isolation,
stale/future/failed/missing inputs, nonfinite/fractional counts, shutdown and
recovery. Regenerate catalogs and preserve existing definitions. Run affected
race/database tests, SQLC, lint and repository policies. VM lifecycle is unchanged;
native metal/leak acceptance is not a gate for this instrumentation.

Adversarial review: token refresh must not masquerade as progress; unknown must
not become zero wait; scrapes must not hold the writer lock; mixed-version writes
must preserve timing; batch limits must not hide backlog; known slow rows must
remain visible alongside unknown history; age warnings must not assert failure.
