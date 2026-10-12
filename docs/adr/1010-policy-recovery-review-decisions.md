# ADR-1010: Policy recovery review decisions and live freshness

Status: Accepted

## Context

ADR-1009 prepares immutable response plans but does not record whether an operator
reviewed them. A saved plan can outlive its target generation or an external
cancellation. Operators need review evidence and an advisory freshness check.

## Decision

Add one immutable decision to each retained policy review with a prepared plan.
The decision is `approved` or `dismissed`, with a required reason, server timestamp
and authenticated actor. Identical retries return the original decision; changing
the decision or reason conflicts. PostgreSQL uses a single atomic UPDATE; MemStore
uses its mutex. Cross-account reads and writes remain unavailable. Review decisions
share parent-job retention and never delete plans or release preparation budgets.
A guarded additive migration refuses downgrade while decisions remain.

Approval records an operator decision. It does not apply a response, reserve
capacity, refresh target generations or guarantee future eligibility. Decisions
can be recorded for stale plans. Dismissal records disposition; explicit manual
application remains possible, as it was for exported files before this feature.
The operator must still preview and explicitly apply any saved plan.

A separate read-only status endpoint checks durable cancellation receipts and
retry child request identities before checking live eligibility. Receipt evidence
therefore takes precedence over a later generation. The aggregate reports `ready`,
`stale`, `unknown`, `partially_applied`, `applied` or `no_plan`, with per-action
reasons. Capacity waits do not invalidate a retry. A retained successful cancellation
or retry child creation counts as applied; this does not claim successful execution
of the retry child. Response-status remains the execution outcome inspector.

Checks are advisory and may observe concurrent changes. Store or receipt identity
errors fail the request rather than inventing readiness. Missing evidence remains
unknown. Applying continues to enforce existing exact generation and receipt checks.
Review decision and live status are deliberately separate: an approved review can
become stale and a dismissed review can have applied actions.

## Consequences

Operators can maintain a durable review trail and identify outdated plans without
mutating execution. This slice does not send review notifications or automatically
execute approved plans. Those remain separate future changes.
