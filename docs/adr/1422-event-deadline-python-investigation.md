# ADR-1422 · Read-only Python checkpoint alert investigation

Date: 2026-10-16
Status: Accepted

## Context

Go, CLI and Node consumers can investigate deadline alerts using current recipient
triage. Python has count/ratio watches and the generated listing operation, but
callers must manually derive compatible filters from a saved alert.

## Decision

Export an async helper accepting the authenticated generated client, a Python
version 1 or 2 checkpoint and an exact incident/sequence reference retained in
pending notifications or acknowledgment history. Reuse full checkpoint validation
and detach selections before yielding. Derive target, cohort, horizon and reason
from the canonical rule. Reject unknown or evicted references before HTTP.

Read one existing triage page with 1..100 recipients, optional opaque continuation
and a cooperative asyncio timeout of at most five seconds. Propagate task
cancellation into the read without background work or partial results. Preserve
normal transport behavior and report non-200 returned responses explicitly.

Validate original response JSON rather than generated datetime projections, keeping
nanosecond ordering. Check scope, read-only coverage, clocks, pagination, identities,
cohort/reason membership, expiry flags and visible sort keys. Reconstruct only known
metadata and keep saved notification time/sample/state separate from current page
observations and traversal classification window.

Use relative horizons only for upcoming count rules. Expired counts select all
expired rows with an optional reason, while ratio investigation covers the expired
numerator only. Preserve opaque live cursor semantics without automatic pagination,
restart, deduplication or aggregate recovery claims.

Reuse endpoint authorization. Add no server route, API schema, database migration,
checkpoint version or delivery mutation. Keep Python checkpoints distinct from
Go/CLI and Node formats. Investigation does not acknowledge or notify an alert.

## Consequences

Python consumers gain the existing investigation workflow without manually rebuilding
filters. A retained checkpoint alert is required; no arbitrary standalone alert
format is introduced. Current recipients can differ from original membership and
cannot certify original alert authenticity, recovery or business completion.

See [usage, bounds and validation](../event-deadline-python-investigation.md).
