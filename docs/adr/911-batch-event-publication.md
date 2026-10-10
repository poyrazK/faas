# ADR-911: Bounded batch event publication

Date: 2026-10-09
Status: Accepted

## Context

Event delivery already captures consumers and recovers them independently. Producers importing events still need one HTTP request per envelope. They need bounded batch ingress without ambiguous deduplication or a failure in one item rolling back unrelated accepted events.

## Decision

Add POST `/v1/events:publish-batch` with the same account authentication, MFA and publication scopes as single-event ingress. Accept 1–100 envelopes in at most 1 MiB of JSON. Invalid outer JSON, oversized bodies and invalid counts reject the request before writes. Decode envelopes independently so malformed item attributes do not prevent valid siblings from being processed. No new plan quota or storage exemption is introduced.

Process items sequentially under a 30-second request budget, using a separate existing customer-publication transaction for each item. Retain schema validation, reserved-source and tenancy checks, storage admission, durable fanout snapshot capture, and identity `(account, source, id)`. Determine duplicate status and read the original acceptance timestamp while holding the same account/identity locks as publication. Memory storage mirrors that critical section. Reuse sqlc queries and the existing schema; no migration is required.

Return HTTP 200 with one result for every zero-based input index. `accepted` and `duplicate` include the durable receipt. `rejected` includes a problem and retryability. `unknown` means durable acceptance could not be confirmed, such as when commit is interrupted; it is retryable. Expiration or cancellation before attempting an item returns retryable `rejected` with `event_publish_not_attempted`. Storage capacity rejections retain the existing limit, observed and retry-after diagnostics. Do not expose internal database errors.

The batch is not atomic. Retry an unanswered request or retryable items with unchanged source, id, type, schema version and data. There is no request-wide Idempotency-Key cache: replaying a partial response must re-evaluate retryable items. Identical duplicates consume no additional storage or fanout work and keep their original receipt position. Conflicting identities fail independently.

New acceptance follows input order, but another request can interleave between item transactions. Duplicates keep their old position and rejected items have none. This does not introduce global execution order: opted-in keyed lanes retain their existing guarantees, and delivery remains at least once.

Expose typed Go, Node and Python SDK operations and `gregale events publish-batch --file file.jsonl` (or `--file -`). The CLI reads one bounded batch before sending, requires explicit stable ids, prints every result, and exits nonzero for rejected or unknown items. It does not generate ids, split files automatically or retry partial results invisibly.

## Consequences

Producers amortize HTTP overhead while each event keeps its own recovery and deduplication contract. A disconnect can leave a partially accepted batch; stable identities make whole-file retries safe within the existing 30-day identity retention window. Accepted means durable ingress, not routing or handler success. Existing scheduler and subscription controls apply without changes.
