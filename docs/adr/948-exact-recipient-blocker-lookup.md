# ADR-948: Exact-recipient routing blocker lookup

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-617, ADR-938, ADR-947

## Context

Bounded subscription backlog pages can omit the delivery under investigation.
Operators need exact evidence and must distinguish missing retention from a
recipient that has left routing, without implying handler completion.

## Decision

Add GET /v1/events/recipient-blocker, requiring source, id, subscription_id and
expected_accepted_at. Identity fields are nonempty and at most 256 bytes; the
acceptance guard is a nonzero RFC3339 instant preserving up to nanosecond precision.
Use the existing account-scoped read permissions, MFA and request limiting.
Return Cache-Control: no-store. Missing and foreign receipts have identical
unavailable responses; caller-supplied account IDs do not select the account.

The PostgreSQL store uses one repeatable-read, read-only transaction. Resolve
one account/source/ID receipt and its captured or backfilled recipient directly;
compare the precise acceptance before inspecting the indexed routing projection
by receipt/subscription primary key. No backlog page, consumer aggregation,
cursor, mutation or replay is involved. Reuse existing ordering and subscription
control functions and the documented waiting-reason precedence. In this exact
lookup an active shared receipt lease takes priority over pending subscription,
ordering and capacity blockers; those metadata remain visible. The memory store supports
the same contract for its retained captured recipients under its existing lock.

Return observed_at, echoed query, status, optional reason/routing_state and the
matched backlog-shaped recipient. Available requires matching retained pending
or processing routing evidence. Not_waiting requires a retained recipient in
enqueued, filtered or failed routing state and no live routing projection.
Missing receipts/snapshots/recipients or inconsistent routing evidence are
unavailable with explicit reasons. A replaced acceptance is acceptance_replaced,
without attaching the replacement's blocker evidence. A five-second server
read deadline returns 503 event_recipient_blocker_read_timeout.

Not_waiting only describes routing. Enqueued handlers may still be running or
failed; filtered/failed routing does not imply successful handler execution.
Neither absence nor not_waiting changes replay attribution or watch settlement.
Recorded next_attempt_at is a schedule, not guaranteed eligibility.

Update opt-in recovery diagnostics in the CLI and Go/Node/Python SDKs to use
exact lookups. Each enrichment clones inputs and permits at most 50 reads,
caching by the full acceptance/recipient identity rather than subscription.
Skip unobserved, failed-read and unsupported recipient rows. Check echoed query,
observation timestamp and matching recipient identity/state before attaching
available evidence; require known settled routing state for not_waiting.
Coverage is exact_recipient. New diagnostics include reason and routing_state;
legacy truncated stays optional and is not emitted by new lookups. No automatic
fallback to page scanning occurs if an older server lacks the endpoint.

Reads share the caller's deadline and cancellation; standalone helpers rely on
caller-supplied cancellation/timeouts. Diagnostic failures remain read_error and
nonfatal, with no diagnostic retries, receipt retry-budget changes, attribution,
status/count/completeness changes, freshness changes or settlement effects.
Callbacks/checkpoints include new snapshots; resume still discards saved ones.

OpenAPI generates Node/Python endpoint clients. The acceptance guard is a string
in that contract to preserve precision in Python; diagnostics inspect wire JSON
so datetime conversion cannot truncate compared fractional timestamps. Go SDK
mirrors the API client implementation and types.

## Consequences

Busy subscriptions no longer hide the exact recipient behind pagination.
Retention loss remains explicit and cannot be mistaken for successful delivery.
Selections larger than the read cap expose budget_exhausted and remain bounded.
Existing ordinary backlog discovery and routing behavior are unchanged; no new
migration is needed because the projection primary key already supports lookup.
Production retention, concurrency and failure qualification remains pending.
No local tests are added or run; compliance registries are unchanged.
