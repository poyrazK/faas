# Scheduler image-to-snapshot handoff diagnostics

Applies to FaasSnapshotPrimeTelemetryUnavailable, FaasSnapshotPrimeWaiting,
FaasSnapshotPrimeEvidenceUnknown and FaasSnapshotPrimeRecoveryQueriesFailing.
All are internal warnings, with no automatic retry, gate bypass or public status
change. Image preparation's handed_off checkpoint does not establish readiness.

## Validate evidence first

Require `up{job="schedd"} == 1` and
`schedd_snapshot_prime_observation_healthy == 1` for the same job/instance.
The observer runs independently of the scheduler work pool, at startup and every
30 seconds, with a three-second read deadline and 120-second freshness ceiling.
Read failures, future/missing/inconsistent data and expired evidence mean unknown,
never an empty backlog. Inspect `sched: snapshot prime handoff observation failed`,
database connectivity, clocks, binary version and scrape configuration.

`schedd_snapshot_prime_observation_enabled == 0` withdraws expectation on loop
cancellation/return. Counts may remain fresh temporarily; zero expectation does
not mean work drained. Failed scrapes belong to target-health diagnostics.

Each fixed class exposes `schedd_snapshot_prime_pending`,
`schedd_snapshot_prime_age_unknown` and
`schedd_snapshot_prime_oldest_attempt_timestamp_seconds`. Unknown age is included
in pending. Oldest covers known times only and is zero if no known ages exist.
The recording `schedd_snapshot_prime_oldest_known_attempt_age_seconds` is zero
for proven empty, absent for unknown-only cohorts or untrusted observations.

| Class | Durable evidence |
| --- | --- |
| waiting | No current snapshot, RAM-holding instance or fresh parked capture; release permits priming |
| release_blocked | Missing, unfinished, failed or incompatible pinned release task |
| active | A post-stage instance holds RAM according to the existing recovery predicate |
| publication_pending | A post-stage parked instance has fresh runtime receipt/config evidence |
| snapshot_recorded | Recovery's selected non-stale snapshot is newer than this stage attempt |
| unknown | Missing/invalid/future stage or activity evidence, or missing artifact key |

Readiness-stage, live/terminal, deleted-app and intentional workload-held rows are
excluded. Owner routing matches the scheduler's explicit owner/default-local
fallback. Legacy empty app owners can overlap scopes: compare instances separately
and never sum replicas. The query covers the whole backlog beyond four recovery
pages, retaining the existing latest-64-instances-per-app lookback.

## Investigate waiting work

FaasSnapshotPrimeWaiting warns when a currently waiting attempt has a known
boundary older than five minutes, held five minutes. Its boundary is no earlier
than current stage start, successful release completion, runtime invalidation and
known instance activity. Release-command execution cannot inflate eligible age.
This is an initial triage threshold, not a continuous idle timer or execution SLO.
A queued or pre-admission prime can lack a durable instance and still be waiting.

Compare recovery list outcomes in `schedd_snapshot_prime_recovery_queries_total`
with `schedd_snapshot_prime_recovery_candidates_total`. A dispatch_requested
decision proves a scheduling request only. Use existing
`schedd_loop_work_total{kind=~"prime|prime_recovery"}` and work duration metrics for
coalescing, overflow, execution and pressure. Recovery keeps its two-minute stage
grace and bounded paging. Logs name candidate inspection/list failures and lost
handoffs; use supported deployment evidence to identify affected rows.

Check release task status and exact artifact, digest, scope, shell and command
pins. A succeeded task with mismatched pins does not permit priming. Check owner
routing, admission pressure and VM/capture logs. Preserve all release, activation,
runtime and ownership fences; do not edit tokens, force phases, delete inputs or
replay a customer release command to quiet a warning.

Fresh parked captures await imaged's snapshot_written processing; they do not
prove artifact publication. Runtime configuration/secret changes can invalidate
that evidence. Old snapshots and parked instances predating rollback's current
stage cannot complete its new attempt. Snapshot selection retains warm-first
preference, including an older warm snapshot over a newer init snapshot.

## Unknown evidence and failures

Missing or invalid stage timing is preserved as unknown; no timestamp is invented
from deployment creation. Waiting rows with missing release/activity times retain
unknown age. FaasSnapshotPrimeEvidenceUnknown warns after 15 minutes of such fresh
evidence. Inspect durable stage/artifact/release/runtime data without rewriting it.
Mixed cohorts still expose oldest KNOWN age, which need not be oldest overall.
Release-blocked unknown ages alone do not trigger this warning.

FaasSnapshotPrimeRecoveryQueriesFailing requires more than 10% errors among
non-canceled listings, at least three errors in every rolling five-minute window,
held five minutes. Independent observer success cannot establish recovery health.
Candidate gates, active captures and caller cancellations cannot trigger it.

See [image preparation](FaasImagePreparation.md),
[build handoffs](FaasBuildImageHandoff.md) and the existing stale-deployment,
work-pool and init-capture diagnostics for adjacent boundaries.

For fresh parked captures or recorded snapshots that still await activation, see
[post-capture publication and activation diagnostics](FaasPostCapture.md).

For release_blocked work, use [app-task runner diagnostics](FaasAppTasks.md) to
distinguish queued commands, restore/execution, cancellation and lease recovery.
A missing or incompatible release task remains a deployment gate finding.
