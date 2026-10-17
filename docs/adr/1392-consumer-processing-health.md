# ADR-1392 · ADR: Retained consumer processing health

- Status: Accepted
- Date: 2026-10-15

## Context

Individual deferred-acknowledgment status does not show a consumer's backlog or
leases approaching expiry. Retained claims support current observations but do
not record rejected renewals or provide a complete historical latency series.

## Decision

Add a read-only processing aggregate to the existing consumer delivery-health
response. Read all matching retained invocations and claim metadata in one query
snapshot (one mutex-held scan in memory). Derive issue time from the claim's fixed
one-hour deadline offset. Count only current matching live claims as processing.
Separate current gauges from windowed retained explicit-completion latency.
Declare rejected-renewal coverage unavailable instead of inventing a zero count.

Reuse the existing local health watch for sustained processing-age, lease-risk
and expiry conditions, recovery events, durable alert queues and fresh checkpoint
resume. Older readers reject unfamiliar metrics. No new route, table, column or
compliance registry is needed.

## Consequences

The aggregate contains neither payloads nor token material and performs no lease
mutation. Retention limits historical interpretation. Sampled expiry conditions
can miss events recovered between polls. Local watches are not a server-side
notification service. See [the metric contract](../event-deferred-acknowledgments.md#consumer-processing-health-and-alerts).

## Validation

Build changed Go packages and clients, lint without tests, validate OpenAPI,
regenerate clients and check SQLC parity. No local tests are added or run under
the user's standing instruction; database concurrency remains unverified locally.
