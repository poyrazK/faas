# ADR 1053: Consumer-group delivery-attempt history

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Persist a bounded group delivery timeline without payloads, metadata, worker names,
credential IDs, lease tokens or hashes. Record sequence, event, attempt, optional
fencing token, safe failure code and database observation timestamp. Event types:
claimed, retried, lease_expired, dead_lettered, deadline_expired, invalid_deadline,
completed and replayed. Renewals, filtered messages, administrative resync/deletion
and failed/stale settlement requests create no timeline observations.

An AFTER UPDATE message trigger observes successful state transitions, including
single/batch retries, single/bulk replay and lazy lease/deadline maintenance.
Terminal lease/deadline failures record the causal event plus dead_lettered. Claimed
and completed events clear unrelated previous failure codes; replay uses reset
attempt zero and preserves the last fencing token for context. Its next lease gets
a new fencing token as usual. Legacy leases can have null fencing tokens.

Successful completion is observed by the existing generated sqlc deletion query,
using its removed row even after the snapshot is gone. Completion, counters,
circuit/metric observation and history commit or roll back together. Message
triggers do not record deletion as completion, so retention/resync cannot manufacture
successful attempts. History survives completion/replay/snapshot retention until
pruned. Group deletion cascades it. No pre-migration backfill or new webhook.

At most 1115 events per group, shared across sequences. Serialize pruning/insertion
under the group lock; remove events older than 24 hours, then retain the newest
1114 before insertion. Idle storage remains bounded and may retain expired rows
until another observation or read; reads prune expired group history using one
transaction-time cutoff. Track a decimal lifetime group_pruned_events count for
age/capacity deletion. This count covers the whole group, including other messages,
and does not identify gaps or causes for an individual timeline.

Expose account GET group `messages/{sequence}/history`, ownership/read scope/MFA
and existing preview checks. Scoped worker credentials cannot read it. Positive
sequence and nonnegative exclusive after_id must fit bigint; default cursor zero,
limit 1..100 default 32. Query one extra row for has_more. Return ordered events,
next_after_id, retention_seconds and group_pruned_events. IDs/cursors/fences are
decimal strings, never JavaScript numbers. IDs are not contiguous for a message;
page order follows IDs rather than wall-clock timestamps. Empty history does not
distinguish never attempted, filtered, completed before migration or pruned work.
Requests return empty history for unobserved sequences in an existing group.

The inspector accepts a delivery sequence with a selected group, shows the safe
UTC timeline, group pruning notice and next page. Timeline errors are separate from
group/history errors. Generated Node/Python SDKs expose the read API. No handler
behavior, retries, ordering, claim admission or lease renewal rules change.

Apply migration 20261012102700520 before the new binary. Update all apid replicas
for completion coverage; older binaries still generate update-trigger observations
but omit completion history. Down removes diagnostic data and pruning counters
without modifying message delivery state; downgrade code with schema. Runtime
SQL/retention/transaction and UI rendering validation remain deferred at user
request. Builds and static generation/spec checks provide compilation evidence only.
