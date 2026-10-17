# ADR-1419 · Read-only Node checkpoint alert investigation

Date: 2026-10-16
Status: Accepted

## Context

Node deadline watches retain count and ratio alerts with validated checkpoint
policy and incident lineage. Go and CLI consumers can investigate current deadline
recipients, while Node consumers must manually derive the matching triage filters.

## Decision

Export a Node helper accepting a version 1 or 2 Node checkpoint and an exact
incident/sequence reference retained in pending notifications or acknowledgment
history. Reuse full checkpoint validation, detach inputs before yielding and
derive target, status, relative horizon and reason from the validated rule.
Reject evicted or mismatched references before reading. Preserve both opening and
resolution notifications without requiring an active incident.

Make one bounded existing triage GET, with an optional opaque cursor, 1..100
recipients and a cancellable read budget of at most five seconds. Validate scope,
coverage, clocks, row/cohort metadata, visible ordering and pagination. Reconstruct
only known payload-free metadata in the returned result. Keep the saved alert,
its checkpoint notification state and its historical ratio sample separate from
current observations and the traversal classification window.

Use upcoming horizon/reason filters for upcoming counts, and expired status without
a relative horizon for expired counts and ratios. Ratio triage covers only the
expired numerator. Preserve live cursor semantics without automatic accumulation,
cursor restart, deduplication or aggregate recovery claims.

Reuse endpoint authentication and authorization. Do not introduce server routes,
API schema, migrations, checkpoint versions or mutations. Node checkpoints remain
distinct from Go/CLI and Python formats. No alert acknowledgment or delivery action
is implied by investigation.

## Consequences

Node users can correlate retained alerts with current recipient metadata without
rebuilding filters. The helper requires a retained checkpoint alert, rather than
accepting an arbitrary standalone alert. Current membership can differ from alert
membership and cannot establish successful delivery or original alert authenticity.

See [usage, bounds and validation](../event-deadline-node-investigation.md).
