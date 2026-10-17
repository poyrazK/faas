# ADR-1414 · Read-only alert-to-deadline investigation in Go

Date: 2026-10-16
Status: Accepted

## Context

Deadline watches identify urgent total/reason cohorts and expired backlog ratios.
Operators must manually map an alert metric to the correct triage status, relative
horizon and reason filter, and can confuse current rows with original membership.

## Decision

Add a Go SDK helper that accepts a Go alert and maps canonical deadline count/ratio
metrics to one existing triage GET. Bind the response to the alert application and
subscription, retain a small correlation reference and label membership as current
at triage observation. Use a fresh relative window for upcoming work and all expired
rows for expired/ratio alerts; do not use notification time as an observation filter.

Require canonical alert scope/incident/sequence metadata, existing page-size/cursor
bounds and a five-second context budget. Validate response coverage, clocks, scope,
row/cohort metadata, visible ordering and pagination before returning a result.
Preserve opaque cursors and live-page semantics. Do not reprove original alert
confirmation or infer successful recovery from returned membership.

Reuse existing endpoint authorization and retention. Do not paginate automatically,
fetch payloads, acknowledge alerts, update controls, retry or replay deliveries.
Add no server route, schema registry entry or database migration. Expose the helper
on the root API client and standalone Go SDK; CLI/other SDK helper exposure remains
separate work.

## Consequences

Consumers can investigate current delivery identities without manually rebuilding
filters. Current observations remain explicitly separate from historical alert proof.
Ratio triage covers the expired numerator rather than recomputing denominator health.
A bounded page is not a frozen cohort, exhaustive search or completion guarantee.

See [usage and validation](../event-deadline-alert-investigation.md).
