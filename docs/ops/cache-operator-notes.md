# Response cache: operator notes

Moved out of the customer page `docs/cache.md` (ADR-20261028200304).

## Distributed caching

Operators can set `FAAS_GATEWAY_RESPONSE_CACHE_REDIS_URL` in
`/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env` to a `redis://` or
`rediss://` URL. The gateway keeps its bounded in-process cache as L1 and uses
Redis as an optional L2. Redis is an optimization, not a source of truth: if it
is unavailable at startup or during a request, Gregale continues with the local
cache and origin.

Cached records expire at the later configured stale boundary. They are not
written to Postgres or gateway disks.
