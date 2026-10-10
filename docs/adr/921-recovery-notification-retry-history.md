# ADR-921: Recovery notification retry decision history

Date: 2026-10-09
Status: Accepted

## Context

An explicit retry request has an immutable request ID and saved per receiver decisions. A client can lose the response, and queued work may later fail or succeed. Repeating the request safely returns the original decision, but it does not directly reveal saved request IDs or current delivery state.

## Decision

Expose a read-only job-scoped history endpoint that lists up to 100 retained decision summaries in decision time order, plus a request-specific detail endpoint. Detail combines the immutable original queued/skipped outcomes and reasons with the latest retained delivery status and replay generation observed in the same read-only repeatable-read transaction or memory lock. If the delivery row has been removed, report it as unavailable; do not change the saved decision. History is deleted with its recovery job and does not outlive existing recovery retention.

Both endpoints require the normal read scope and MFA, reject query parameters, and return only target IDs, decision metadata, states, reasons, and current delivery status. They omit request actor identity, endpoint URLs, payloads, response bodies, and secrets. Listing validates and reads the bounded receipt object; detail reads one saved request and the existing metadata-only notification evidence. The Go client, generated Node/Python SDKs, and CLI provide summary and detail views.

## Consequences

Operators can find an uncertain request ID in history and inspect what each receiver was originally asked to do, then compare it with the latest retained delivery generation. The stored idempotency response remains the authority for replaying the same request. No new storage, migration, retention schedule, or automatic retry behavior is introduced.
