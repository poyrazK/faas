# ADR-1390 · Durable windowed deferred acknowledgment metrics

Status: accepted

## Context

Coalesced diagnostic outcome groups cannot split renewals accurately across
query windows. Rates derived from those groups would misattribute operations.
Invocation pruning must not erase recent operational counts, and invalid-token
traffic must not create metric rows.

## Decision

Record three operation kinds in durable per-account/app/consumer UTC minute
buckets: explicit renewal success, verified current-token renewal rejection and
deferred-awaiting scheduler expiry transition. Write in the existing invocation
transaction after token and attempt checks. Bound each bucket with a saturating
counter and retain buckets for 48 hours; read filters and bounded scheduler
cleanup enforce retention. Sort expiry processing by account/app/consumer within
the existing expiry-prioritized batch, locking each account quota before its
shared metric counters even when its first selected attempt is unreserved.

Attach a typed summary to the existing processing health object and CLI view.
Select whole minutes wholly inside the requested window, excluding the current
minute. Return effective bounds, sample counts, a recorded-operation rejection
fraction and expiry events per second. Omit rates for empty or saturated windows
and omit rejection fraction without renewal samples. Keep the legacy non-windowed
coverage enum unchanged for existing SDK compatibility.

## Consequences

At most three rows per consumer per retained minute are needed, regardless of
renewal frequency. Invocation pruning does not erase counts. Counters are
payload-free and contain no per-delivery identity. App/account deletion removes
them; there is no subscription foreign key that could block already accepted
work after subscription deletion. Counts describe recording by upgraded writers,
not reconstructed pre-upgrade or mixed-version activity, unique-delivery failure
rates or historically complete SLOs. Delayed commits can affect later reads of a
closed bucket. Physical cleanup can lag, and recording failures fail the enclosing
operation. Existing alerts remain unchanged until a later feature defines sample
and rollout requirements for these metrics.
