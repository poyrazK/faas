# ADR-1404 · Persisted application outbox retry policies

Date: 2026-10-15
Status: Accepted

## Context

Worker-local retry settings can disagree across processes and deployments. A
publication needs a stable retry contract, while existing unbound rows require an
explicit adoption path that preserves their identity, accumulated claims and
original retention/retry window.

## Decision

Bind opt-in version-1 retry policies in an application-owned side table, saved in
the caller's business transaction. Persist normalized millisecond backoffs,
attempt limit, semantic version, current revision and immutable initial digest.
Repeated staging validates the initial digest without resetting audited updates.

Read policy under the existing publication row lock and capture it for the whole
claim. Bound settings override worker retry settings; unbound rows retain their
fallback. Invalid/unknown policies and unavailable policy storage fail closed.
Operational request/lease timeouts remain worker configuration.

Add an always-audited set_retry_policy command with updated-time, attempt and
revision guards. Require unresolved/unexpired work without a live claim, preserve
state and scheduling, and commit the new revision, metadata update and result
receipt together. Adoption expects revision zero. Granting budget does not resume
blocked work, reset attempts or extend deadlines.

Provide opt-in local DDL and a transactional CHECK upgrade. Drain older workers
before adopting bindings; no silent migration backfill guesses prior configuration.
The side table cascades with publication pruning, while history remains separate.
Parameterized application SQL follows the existing static SQLC validation
exception; no platform migration or endpoint is introduced.

## Consequences

Supported workers consistently recover a bound publication across restarts.
Applications must coordinate schema/SDK rollout and explicitly adopt old rows.
Prior workers cannot enforce bindings, and missing storage cannot safely use
fallback. Control receipts retain snapshots rather than current policy assertions.
Identity, retry windows, remote acceptance retention and business deduplication
remain unchanged.

See [binding, control and upgrade guide](../event-outbox-stored-retry-policy.md).
