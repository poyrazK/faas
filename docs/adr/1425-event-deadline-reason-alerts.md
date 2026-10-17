# ADR-1425 · Independent deadline alerts by observed waiting reason

Date: 2026-10-16
Status: Accepted

## Context

Complete deadline-window responses already partition upcoming and expired work by
waiting reason. Total count alerts cannot distinguish a paused or capacity-blocked
cohort whose urgency is below the overall threshold but still needs attention.

## Decision

Extend Go deadline-window watch options with separate upcoming and expired reason
threshold maps. Accept only known reason names and bounded integer counts; zero
or omission disables a known key, while unknown names are rejected even at zero.
Require at least one enabled total or reason condition. Clone option maps before
polling, sort positive selections deterministically, and preserve existing total
metric identities and order.

Encode cohort, canonical horizon and known reason in each new metric identity.
Reuse the existing integer-count warning/recovery state machine and checkpoint
validation, independent incidents and durable alert outbox. Require full existing
window evidence and exact known-reason partitions before reading any selected
count. Missing evidence remains unknown rather than zero. Reuse the same aggregate
read for all selected reasons, without server, schema or endpoint changes.

## Consequences

Operators can monitor reason-specific urgency independently of total counts.
Reason changes and aging into expiry can resolve one condition without completing
work, so those limits and the possibility of overlapping total/reason incidents
are explicit. Checkpoint resume requires the same enabled configuration; map
insertion order is irrelevant. Existing clients without reason selections retain
request behavior and checkpoint identities. This slice changes only Go watch
helpers and documentation; Node/Python aggregate APIs remain available unchanged.

See [configuration and recovery semantics](../event-subscription-deadline-window.md#alerts-for-selected-waiting-reasons).
