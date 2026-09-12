# ADR-156 · Request-aware outbound integration admission

- **Status:** accepted
- **Date:** 2026-09-12
- **Decision:** Provide an explicit HTTP gateway path for opted-in third-party
  integrations. Admission state is shared through Postgres, with a token
  budget and expiring concurrency leases scoped to the integration.

## Context

Network namespaces currently enforce packet-level egress policy and bandwidth,
but a provider's API quota is request-aware. If twenty instances each keep a
local limiter, an account-wide 50 requests/second provider budget can still be
exceeded. Gregale also cannot safely inspect arbitrary encrypted traffic or
replay an unknown request.

## Decision

Applications send requests to `/i/{integration_id}/...` and include an
integration token plus their app ID. The gateway resolves a fixed HTTPS origin,
checks the token and app attachment, and atomically asks a shared admission
backend for:

1. one token from the integration's rate/burst budget; and
2. one expiring in-flight lease.

The integration token is a Gregale admission credential. Provider credentials
remain application-owned in v1; ordinary provider headers are forwarded while
`X-Gregale-*` control headers are removed before the upstream request.

The Postgres backend locks one state row per integration, removes expired
leases, refills the token bucket using database time, and commits a lease before
the provider request is sent. A failed backend operation is fail-closed.

Over-budget requests receive RFC 7807 `429` responses with `Retry-After` and
`X-Gregale-Outbound-Rejection` (`rate_limit` or `concurrency_limit`). The
gateway makes one upstream attempt, does not follow redirects, strips internal
headers, and releases the lease when the response completes or the request
context is cancelled. Provider responses, including provider `429`s, are
passed through unchanged.

Configuration and app bindings are stored in the `outbound_integrations` and
`outbound_integration_apps` tables. A later extension may add durable scheduling
for callers that prefer queueing; v1 rejects predictably instead.

## Rejected alternatives

- Transparent packet interception: cannot identify request boundaries in
  encrypted traffic and would make credentials and replay semantics ambiguous.
- Per-instance limiters: do not enforce one provider budget across instances.
- Automatic retries: unsafe for non-idempotent requests and surprising to the
  application; callers own retry policy after `Retry-After`.
