# ADR-1394 · Deferred acknowledgment outcome history

Status: accepted

## Context

Current processing health cannot explain past lease renewals or expiry. Tokens
and payloads must stay out of diagnostics, and valid-token request repetition
must not append unbounded rows.

## Decision

Use an operational table keyed by invocation, attempt, replay generation and
five fixed outcome kinds. Coalesce first/last timestamps and a saturating count.
Record transitions in the locked invocation transaction. A current-token renewal
rejection commits its diagnostic before returning the existing conflict error;
invalid or replaced tokens never write. Invocation ownership scopes access and
invocation deletion cascades to history. Expose a read-only consumer route with
an optional delivery filter, at most 100 groups and explicit truncation.

Retention ends seven days after the fixed attempt deadline, enforced on reads
and by bounded scheduler cleanup. Repetition cannot prolong retention. This is
a diagnostic ledger starting at rollout, not an audit log or exact windowed rate
source. Existing health metrics and alerts remain unchanged.

## Consequences

Storage has at most five rows per retained attempt, regardless of renewal count.
Repeated calls still incur writes; existing authentication and API rate limits
remain necessary. Invocation pruning shortens history, and cleanup can lag when
the scheduler is unavailable. Pagination and window-aligned summaries can follow
separately. Storage failures prevent transitions rather than lose diagnostics.
