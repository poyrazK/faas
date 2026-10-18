# ADR: Adaptive Go acknowledgment intent retry scheduling

- Status: Accepted
- Date: 2026-10-17

## Context

Saved-callback recovery uses a fixed persisted delay in Go, Node and Python.
During an outage, many workers can retry together. The existing intent table
already retains a saturating recovery-claim count and a database-clock due time.
Go needs an optional adaptive schedule that preserves the callback protocol and
the fixed-delay API contract.

## Decision

Add `EventAcknowledgmentIntentRetryPolicy` with initial and maximum caps, default
one second and five minutes. Validate whole-millisecond values from one
millisecond through one hour, with maximum at least initial. Add value-policy
delivery and single-iteration entry points alongside existing fixed-delay APIs.
The bounded pass accepts an optional policy pointer, copies it before I/O and
rejects combining it with a nonzero fixed backoff.

Read the updated `delivery_attempts` using the claim update's `RETURNING` clause
inside the existing transaction. Use that committed count for capped exponential
full jitter, doubling only until reaching the cap. Derive jitter through a
domain-separated hash of the already-random claim token so no new entropy read
can fail after an uncertain callback. Select one millisecond through the cap.

Reuse the existing delivery/settlement path. Persist a new due time only for an
unconfirmed settlement, using database now plus the delay and the existing live
claim condition. Keep receipt checks, saved hashes/actions, platform fences,
callback-level retries, cancellation and uncertain-commit handling intact.

## Consequences

The schedule and attempt count survive worker changes. Worker configuration
governs future settlements; policy values are not bound to rows. Applications
must coordinate configuration, especially across languages still using fixed
backoff. Recovery claims count crashes and uncertain commits, rather than exact
HTTP sends. A failed settlement can leave the previous due time in place.

The application-owned schema remains unchanged. There is no lifetime exhaustion
or automatic negative action, and durable scheduling does not consume remote
Retry-After guidance. Existing callback-level bounded Retry-After behavior is
preserved. See the [policy contract](../event-acknowledgment-go-retry-policy.md).

## Validation

Standalone Go SDK/example builds and vet passed. Source inspection confirmed
bounded pass admission/join behavior and byte-identical receipt/callback/fenced
settlement code. Go, Node and Python intent schemas remain unchanged and equal.
Shared schema generation parity, ADR uniqueness and documentation checks passed.
No tests or test assertions were added/changed, and no local tests or SDK helper
smoke checks were run. Hosted timing, restart, concurrency, cancellation and
uncertainty qualification remains pending.
