# ADR-722: Check workflow transition continuity in the application transaction

## Status

Accepted

## Context

The customer SDK allocates a revision per workflow run and stores state reports
in the same application database transaction as the business write. The
application also checks a transition's source state against its locked business
row. Before this decision, the SDK accepted a `from_state` that disagreed with
the latest state report it had already saved for that run.

## Decision

The existing per-run revision counter stores a nullable `last_state`. When
saving a transition with `from_state`, the SDK reads that value for the same
tenant, workflow, and instance. If a prior state exists, it must match
`from_state`, or the transaction fails before commit. The SDK performs this
check after incrementing the counter. Its row lock serializes reports for the
run, including multiple transitions within one transaction.

If no prior report exists, the SDK permits the first transition report. The
application's locked business row remains the source of truth for that initial
state. Snapshot reports without `from_state` remain valid and become the
predecessor checked by a later transition. Installing the updated
`customerOperationReceiptSchema` adds the counter column and backfills it from
retained reports, so cleanup of older outbox rows does not erase the latest
known state. No Gregale platform schema change is required.

## Consequences

- Conflicting transitions across Operations fail with the business write and
  outbox together, instead of publishing an inconsistent app-side history.
- Concurrent transactions for the same workflow run check their source state
  in revision order.
- First-time or imported workflows retain an explicit bootstrap path backed by
  the application's own row lock.
