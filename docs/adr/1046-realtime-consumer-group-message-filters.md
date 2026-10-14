# ADR 1046: Immutable consumer-group message filters

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add optional creation `filter` with `event_types` and exact `metadata` predicates.
Defaults are an empty list/object, matching all events and preserving existing groups.
Event types compare case-sensitive values of `metadata.event_type`, not the retained
message's created/updated/deleted lifecycle `event`. At most 32 unique nonempty,
trimmed event names, each at most 128 bytes without NUL/CR/LF. Metadata uses existing
validation: at most eight valid lowercase identifier keys of 1–64 bytes, values
at most 128 bytes without NUL/CR/LF, and 4096 encoded bytes. Canonical whole filters
are bounded at 8192 encoded JSON bytes. Increase only group-creation request body
admission to 16384 bytes to accommodate filters alongside existing configuration.

A nonempty event-type list matches any listed type (OR). Every metadata equality
predicate must match, and both filter sections must match (AND). Missing metadata
never equals an expected empty string. No wildcards, regex, coercion, payload queries
or case normalization are introduced. Predicates apply independently to all retained
history rows, including updates and deletions with their stored metadata. Filters
are selection controls, not an authorization boundary or deduplication mechanism.

Filters are immutable through the public API, like ordering identity. Configure
on group creation in the same transaction as the new group. Existing groups default
to match-all; recreate a group with an explicit retained starting sequence to change
selection. Assignment reads each next retained event under existing group/head locks.
A nonmatch atomically advances assigned_sequence and increments decimal-string
`filtered_messages`, without creating an outstanding snapshot. It consumes no lease,
retry attempt, fencing token or claim-rate credit and is not a successful completion.
Matched rows continue through existing snapshot, ordering, deadline and claim logic.
Retries and replay operate on their already admitted immutable snapshots.

Skipped rows cannot become ordering predecessors; matching snapshots still preserve
relative per-key ordering. Retention gaps remain errors before evaluating filters,
so lost events cannot be silently assumed irrelevant. Explicit resync skips are not
added to the filter counter. Lag/unassigned metrics remain based on the raw channel
cursor until scanning proves rows irrelevant; they are not precomputed filtered
backlog estimates. Existing pause/drain, lease capacity and rate admission can stop
assignment progress, including during a nonmatching prefix.

## Bounded progress and visibility

At most 128 assignment scans per batch transaction, shared across its claims and
including matching rows. Already assigned ready work does not consume scan budget.
On exhaustion, commit cursor/counter progress and any partial claims, returning
`filter_scan_limited: true`. The flag reports an exhausted budget, not proof of more
matching work. Long polls return immediately on this condition; clients can poll
again. Single claims with no claim return their existing 204. Node helpers use their
normal empty-work polling behavior, while batch clients can see explicit scan state.
Unexpected errors and transaction failure roll back cursor/counters/new snapshots
and claims together. Full snapshot capacity still stops further assignment.

Group status and the inspector expose the canonical filter and cumulative skipped
counter. Counters survive completion, replay, pause and resync, and reset only with
a new group. No webhook is emitted for deliberate filter exclusions. New initial
configuration and skip updates use generated sqlc methods with the existing
migration-owned table projection.

## Rollout and verification

Apply migration `20261012102700506_realtime_consumer_group_filters.sql`, and upgrade
all server replicas before creating filtered groups. Older replicas do not honor
selection. Existing groups continue matching everything. Upgrade SDKs to configure
filters and consume the additive group/batch fields. Rollback removes configuration
and counters but cannot rewind assignment cursors or recover skipped snapshots;
future assignment resumes match-all. Stop filter-dependent consumers before rollback
and recreate groups explicitly from retained history if recovery is required.

OpenAPI and generated Node/Python SDKs expose configuration, counters and scan flags.
Go/Node builds, OpenAPI parity, Python static checks and SQLC generation parity are
used for this change. Runtime tests, migration execution and inspector rendering
remain deferred at the user's request.
