# ADR: Guarded Go acknowledgment retry-policy adoption

- Status: Accepted
- Date: 2026-10-17

## Context

All SDKs bind policies at new staging and honor them during recovery. Existing
unbound backlog remains governed by worker fallback. Explicit policy staging
conflicts with an unbound duplicate. Operators need a guarded binding operation
that preserves the saved callback and resolves interrupted command commits.

## Decision

Add `AdoptEventAcknowledgmentIntentRetryPolicy` in Go. Validate/copy the policy,
owner, invocation/generation/attempt, command UUID and expected timestamp/count
before I/O. Serialize each owner/command UUID and replay a matching retained
receipt before checking the current row. Reject changed requests under that ID.

For new commands take staging's invocation lock, then the intent row lock shared
with recovery. Bind only unresolved, unbound work at the expected timestamp and
claim count without a live database claim. Preserve action, protected inputs,
count, due time, state and expired claim metadata. Set policy columns and strictly
advance `updated_at`. An expired claim cannot settle. Existing bound policies
cannot be revised, removed or adopted again through another command.

Commit a payload-free historical status and command receipt atomically with the
binding in a separate optional application-owned table. Retain missing/conflict
outcomes too, without snapshots. A failed COMMIT returns the existing unknown
commit sentinel and no receipt. Repeating exact original inputs resolves the
retained outcome or attempts the original operation again after rollback.
No HTTP, business work or platform renewal occurs. Driver failures are sanitized.

## Consequences

Applications install the optional receipt DDL after the policy-column upgrade;
ordinary staging/recovery requires no additional schema. Upgrade all recovery
hosts before enabling adoption. Updated Node/Python recovery honors adopted
bindings; their adoption API parity remains subsequent work. The application owns
authorization, command identity and receipt retention. Receipts survive intent
pruning, and deleting receipts loses command deduplication. Historical success is
not current ownership or remote confirmation. See the
[adoption contract](../event-acknowledgment-policy-adoption.md).

## Validation

Standalone Go SDK/example builds and vet passed. SQLC statically compiled all six
adoption SQL calls against both fresh and upgraded application schemas, without a
database. Existing shared schema generation parity, ADR uniqueness and docs route
checks passed. Staging, claim, callback and settlement implementations are unchanged.
No tests or assertions are added or changed, and no local tests or SDK helper smoke
checks are run. Hosted behavioral qualification remains pending for concurrency,
replay, guard rejection, supersession, cancellation, rollback and unknown commits.
