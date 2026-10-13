# ADR 1058: Background dead-letter replay jobs

Status: Accepted

## Context

Bulk dead-letter replay requires operators to manage selections and repeated requests during recovery. Replaying all failures immediately can overload a repaired worker.

## Decision

Add account-owned durable replay jobs. Creation atomically captures current matching failure identities (group UUID, sequence, exact dead_since), bounded to 128, with optional failure-code and sequence filters. Empty selections are rejected. The immutable capture excludes later failures. Account ownership, MFA and deploy-write scope authorize creation and controls; scoped worker credentials cannot manage jobs. Four running or paused jobs per group, with twelve retained terminal jobs. Creation is not idempotent: inspect recent jobs after an uncertain response before creating another.

A PostgreSQL job row records selection, progress, per-item outcomes and a rate upper limit of 1–32 attempts per second. Each pass processes one item per due job, always locking the group before the job. Replaying and advancing progress commit in the same transaction. Due status is rechecked after acquiring locks, making concurrent API replicas safe. No accumulated rate credit or catch-up bursts. Scheduling, replica contention and database latency can make actual rates lower. Multiple jobs have independent limits; worker claim rate remains the aggregate processing control.

Reuse bulk replay's identity, ordering, deadline and lease checks. Known skips are terminal per item and require a new job after fixing the blocker. Unexpected database errors roll back the transaction, preserve the position, record a safe processing_failed diagnostic and increment a cumulative error count with a five-second retry delay where the database remains reachable. Error counters are operational observations, not exactly-once accounting. Payloads and credential secrets are never copied into jobs.

Pause/resume/cancel serialize on the same group and job locks. Cancellation only prevents future transitions and does not retract committed replays. Disabled endpoints, suspended accounts and deleted applications suspend background execution. A job is durable account intent and survives expiration/revocation of the creating API token. Group pause and circuit gates continue to govern claims, while replay can prepare ready messages. Group deletion cascades jobs; resync preserves them and may cause missing/stale outcomes. Nominal retention may remove selected failures before replay.

Expose list/create/control API operations, Node/Python SDK methods, and inspector forms protected by ownership, scope, MFA and a named authenticated CSRF envelope. Recent jobs expose counts, safe processing diagnostics and individual outcomes. Active jobs sort first. Terminal history is pruned on subsequent creation.

## Consequences

The worker runs inside the API process behind the realtime preview gate. All API replicas need the new schema and binary before enabling jobs. The migration is additive and unapplied during development. Downgrade refuses running/paused jobs; cancel them first. Replay only changes queue readiness and does not guarantee external exactly-once processing. Runtime tests, migration application and inspector rendering are deferred at the user's request; compile and static checks cover only build compatibility.
