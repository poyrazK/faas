# ADR-211 · Distributed declarative response caching

- **Status:** Accepted
- **Date:** 2026-09-22
- **Decision:** Extend ADR-122's `kind=cache` with general
  stale-while-revalidate and an optional Redis-backed shared L2. The bounded
  in-process LRU remains L1 and the complete fallback.

## Context

ADR-122 shipped safe per-route response caching, deterministic keys, explicit
purge hooks, and stale-on-error. Its store was intentionally local to each
`gatewayd-internal`, and its stale-while-waking behavior only covered cold-app
wakes. A multi-gateway fleet therefore warms the same response independently,
and a warm origin cannot opt into the usual stale-while-revalidate latency
tradeoff.

## Decision

`EdgeRuleCacheAction` gains `stale_while_revalidate_seconds`, defaulting to 0
and capped at 300 seconds. Once freshness expires inside that window, the
gateway returns the cached response with `Warning: 110`, records a distinct
outcome, and starts a detached refresh. Refreshes are singleflight-coalesced by
the complete cache key.

When `FAAS_GATEWAY_RESPONSE_CACHE_REDIS_URL` is configured, each gateway reads
L1 first and Redis second, hydrates L1 from a shared hit, and writes through to
both. Redis keys are namespaced, app-partitioned, and hash the complete cache
key. Records expire at the later SWR or stale-if-error boundary. App, path,
rule, and deployment invalidations apply to both tiers.

Redis is never authoritative. Startup connection failure leaves the gateway in
local-only mode; operation failure is treated as an L2 miss and requests
continue to L1 or origin. Response bodies are not written to Postgres or local
disk. Credentialed requests and responses carrying session/private/no-store
signals retain ADR-122's hard bypass.

## Consequences

- Gateway instances share hot public responses and explicit purges.
- SWR avoids request latency while refresh work is coalesced.
- Redis becomes an optional operational dependency, but never an availability
  dependency for request serving.
- Shared response bodies broaden the cache's data-processing surface. TTLs,
  app-partitioned keys, hard credential bypass, and explicit purge bound that
  exposure; durable persistence remains out of scope.

Rollback is operationally simple: unset the Redis URL and restart gateways.
The L1 behavior remains available and abandoned Redis records expire by TTL.
