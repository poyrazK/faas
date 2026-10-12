# ADR 1048: Consumer-group processing latency metrics

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Expose an account-owned GET group `metrics` route using read scope, MFA and the
existing endpoint ownership/preview checks. Scoped worker credentials cannot read
this route. Generated Node/Python SDKs expose the account operation. The inspector
shows one row per minute for the last hour without retaining message payloads or
worker identities in telemetry.

Return 60 UTC minute buckets, oldest first, including the current partial minute.
Empty minutes are zero-filled. Counts are decimal strings to preserve bigint
precision; averages are nonnegative floating-point milliseconds. Zero with zero
samples means no observations. There are no inferred percentiles.

Queue wait is publication time to the first observed claim, including backlog and
ordering delay. Capture once per snapshot; retry/replay does not reset that flag.
Pre-migration attempted rows are marked already observed, with no fabricated
first-claim sample. Never-claimed legacy snapshots can still produce a sample.

Processing time is lease creation to successful completion of that attempt;
renewals preserve the start. Failed attempt time and retry delays are excluded.
Completed counts are measured successful attempts, not the group's lifetime
completed counter. Legacy in-flight attempts lack a start and are excluded until
another attempt is leased. No exactly-once inference is made from these metrics.

Retries count leased-to-ready transitions, including expired leases. Dead letters
count transitions into dead, including invalid/expired deadlines. Replay may
produce another completion or dead-letter transition. Filter skips, administrative
resync/deletion and dead-letter retention deletion emit no observations. Lazy lease
or deadline expiry is bucketed when maintenance observes it, not retroactively.
Retry/dead-letter counts are event rates per minute, not failure probabilities.

A BEFORE UPDATE trigger maintains attempt starts and aggregates state transitions.
The generated sqlc completion query atomically deletes the valid lease and records
its successful duration in the same transaction. Group locking serializes updates
across workers and replicas; failed transactions roll telemetry back as well.

A cascading aggregate table retains at most 60 occupied minute buckets per group.
Each observation prunes buckets outside the current window, including future rows
if the database clock moves backward. Idle groups may retain stale buckets until
the next observation, but reads exclude them and storage remains bounded. No
background sampler or additional notification is needed. Deleting a group deletes
its telemetry. Resync retains recent aggregate history with the same group UUID.

Migration 20261012102700512 adds the aggregate table and per-snapshot timing fields.
Deploy it before the new server binary. Rolling older binaries do not report
completion metrics, although the update trigger still observes their transitions;
upgrade all apid replicas for complete coverage. Down discards telemetry and timing
without changing delivery state. Validate trigger/CTE behavior against Postgres
before release; builds and static checks alone do not provide runtime evidence.
