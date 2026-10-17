# ADR-1429 · Complete deadline-window counts and urgency alerts

Date: 2026-10-16
Status: Accepted

## Context

Full pending summaries expose the earliest deadline and total expired recipients,
and deadline triage identifies individual deliveries. They cannot yet quantify
upcoming deadline urgency over a selected horizon or partition those cohorts by
observed waiting reason. A boolean earliest-deadline alert does not measure size.

## Decision

Add a read-only subscription deadline-window operation with a mandatory horizon
of 100 ms through 24 h. Return the full pending summary, an upcoming count for the
strict interval (observation, window end), and separate exact waiting-reason
partitions for upcoming and already-expired cohorts. Reuse effective-deadline and
backlog waiting-reason rules, one database clock and a Repeatable Read snapshot.
Aggregate without a sampling cap, bound response groups and in-memory counters,
and preserve the five-second request/four-second statement budgets. Failed reads
return no partial counts and incomplete evidence cannot confirm health.

Add optional Go watch upcoming and expired count conditions under distinct metric
identities containing the horizon. Reuse sustained opening/recovery, durable
checkpoints and alert delivery. Deep-copy the nested option before polling and
progress maps before callbacks. Replace the separate summary read with the nested
same-snapshot summary when selected, preserving old request behavior otherwise.
No sampled fallback or automatic recovery action is introduced.

Add relative-window and observed waiting-reason filters to deadline triage before
pagination. Anchor the relative horizon to the first page clock, keep reasons live,
and bind filters to cursor scope. Omit absent new fields from the existing scope
encoding so unfiltered cursors retain their meaning. Reject expired status with a
relative upcoming window.

## Consequences

Operators can quantify urgency and investigate observed contributors through Go,
Node and Python APIs. Go watch count conditions can distinguish future urgency
from accumulated expiry. An upcoming count can resolve by aging into expiry; this
is explicitly not delivery success and the independent expired condition preserves
that signal. Fixed time budgets can still yield unknown on large backlogs. No
schema migration or production DDL is required. Existing alert metric identities,
ordering, retention, retries, replay and acknowledgement behavior remain intact.

See [count, alert and triage semantics](../event-subscription-deadline-window.md).
